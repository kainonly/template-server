package admin

import (
	"context"
	"github.com/bytedance/go-tagexpr/v2/binding"
	"github.com/bytedance/go-tagexpr/v2/validator"
	"github.com/bytedance/gopkg/util/logger"
	"github.com/bytedance/sonic/decoder"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/errors"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/google/wire"
	"github.com/weplanx/transfer"
	"github.com/weplanx/utils/csrf"
	"github.com/weplanx/utils/helper"
	"github.com/weplanx/utils/passport"
	"github.com/weplanx/utils/resources"
	"github.com/weplanx/utils/sessions"
	"github.com/weplanx/utils/values"
	"net/http"
	"os"
	"server/admin/index"
	"server/admin/tencent"
	"server/common"
	"time"
)

var Provides = wire.NewSet(
	index.Provides,
	values.Provides,
	sessions.Provides,
	resources.Provides,
	tencent.Provides,
)

type API struct {
	*common.Inject

	Hertz     *server.Hertz
	Csrf      *csrf.Csrf
	Transfer  *transfer.Transfer
	Values    *values.Controller
	Sessions  *sessions.Controller
	Resources *resources.Controller

	Index   *index.Controller
	Tencent *tencent.Controller
}

func (x *API) Routes(h *server.Hertz) (err error) {
	release := os.Getenv("MODE") == "release"
	csrfToken := x.Csrf.VerifyToken(!release)
	auth := x.AuthGuard()

	h.GET("", x.Index.Ping)
	h.POST("login", csrfToken, x.Index.Login)
	h.GET("verify", x.Index.Verify)
	h.GET("code", auth, x.Index.GetRefreshCode)
	h.GET("options", x.Index.Options)

	universal := h.Group("", csrfToken, auth)
	{
		universal.POST("refresh_token", x.Index.RefreshToken)
		universal.POST("logout", x.Index.Logout)

		helper.BindResources(universal, x.Resources)
		helper.BindValues(universal, x.Values)
		helper.BindSessions(universal, x.Sessions)
	}

	_user := h.Group("user", csrfToken, auth)
	{
		_user.GET("", x.Index.GetUser)
		_user.POST("", x.Index.SetUser)
		_user.DELETE(":key", x.Index.UnsetUser)
	}

	_tencent := h.Group("tencent", auth)
	{
		_tencent.GET("cos_presigned", x.Tencent.CosPresigned)
		_tencent.GET("cos_image_info", x.Tencent.ImageInfo)
	}

	return
}

func (x *API) AuthGuard() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		ts := c.Cookie("access_token")
		if ts == nil {
			c.AbortWithStatusJSON(401, utils.H{
				"code":    0,
				"message": "authentication has expired, please log in again",
			})
			return
		}

		claims, err := x.Index.Service.Verify(ctx, string(ts))
		if err != nil {
			c.SetCookie("access_token", "", -1, "/", "", protocol.CookieSameSiteLaxMode, true, true)
			c.AbortWithStatusJSON(401, utils.H{
				"code":    0,
				"message": index.MsgAuthenticationExpired,
			})
			return
		}

		c.Set("identity", claims)
		c.Next(ctx)
	}
}

func (x *API) AccessLogs() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		now := time.Now()
		c.Next(ctx)
		method := string(c.Request.Header.Method())
		if method == "GET" {
			return
		}
		var userId string
		if value, ok := c.Get("identity"); ok {
			claims := value.(passport.Claims)
			userId = claims.UserId
		}
		x.Transfer.Publish(context.Background(), "access", transfer.Payload{
			Timestamp: now,
			Metadata: map[string]interface{}{
				"method":    method,
				"path":      string(c.Request.Path()),
				"user_id":   userId,
				"client_ip": c.ClientIP(),
			},
			Data: map[string]interface{}{
				"status":     c.Response.StatusCode(),
				"user_agent": string(c.Request.Header.UserAgent()),
			},
		})
	}
}

func (x *API) ErrHandler() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		c.Next(ctx)
		err := c.Errors.Last()
		if err == nil {
			return
		}

		if err.IsType(errors.ErrorTypePublic) {
			statusCode := http.StatusBadRequest
			result := utils.H{"message": err.Error()}
			if meta, ok := err.Meta.(map[string]interface{}); ok {
				if meta["statusCode"] != nil {
					statusCode = meta["statusCode"].(int)
				}
				if meta["code"] != nil {
					result["code"] = meta["code"]
				}
			}
			c.JSON(statusCode, result)
			return
		}

		switch e := err.Err.(type) {
		case decoder.SyntaxError:
			c.JSON(http.StatusBadRequest, utils.H{
				"code":    0,
				"message": e.Description(),
			})
			break
		case *binding.Error:
			c.JSON(http.StatusBadRequest, utils.H{
				"code":    0,
				"message": e.Error(),
			})
			break
		case *validator.Error:
			c.JSON(http.StatusBadRequest, utils.H{
				"code":    0,
				"message": e.Error(),
			})
			break
		default:
			logger.Error(err)
			c.Status(http.StatusInternalServerError)
		}
	}
}

func (x *API) Initialize(ctx context.Context) (h *server.Hertz, err error) {
	h = x.Hertz

	h.Use(x.AccessLogs())
	h.Use(x.ErrHandler())
	helper.RegValidate()

	updated := make(chan *values.DynamicValues)
	go x.Values.Service.Sync(&values.SyncOption{
		Updated: updated,
	})

	if err = x.Transfer.Set(ctx, transfer.LogOption{
		Key:         "access",
		Description: "Access Log Stream",
	}); err != nil {
		return
	}

	go func() {
		for {
			select {
			case <-updated:
				if err = x.Resources.Service.Load(ctx); err != nil {
					return
				}
			}
		}
	}()
	return
}