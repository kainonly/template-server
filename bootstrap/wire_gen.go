// Code generated by Wire. DO NOT EDIT.

//go:generate go run github.com/google/wire/cmd/wire
//go:build !wireinject
// +build !wireinject

package bootstrap

import (
	"github.com/weplanx/rest/api"
	"github.com/weplanx/rest/common"
)

// Injectors from wire.go:

func NewAPI(values *common.Values) (*api.API, error) {
	client, err := UseMongoDB(values)
	if err != nil {
		return nil, err
	}
	database := UseDatabase(values, client)
	redisClient, err := UseRedis(values)
	if err != nil {
		return nil, err
	}
	conn, err := UseNats(values)
	if err != nil {
		return nil, err
	}
	jetStreamContext, err := UseJetStream(conn)
	if err != nil {
		return nil, err
	}
	keyValue, err := UseKeyValue(values, jetStreamContext)
	if err != nil {
		return nil, err
	}
	inject := &common.Inject{
		V:         values,
		Mgo:       client,
		Db:        database,
		RDb:       redisClient,
		JetStream: jetStreamContext,
		KeyValue:  keyValue,
	}
	hertz, err := UseHertz(values)
	if err != nil {
		return nil, err
	}
	service := &api.Service{
		Inject: inject,
	}
	controller := &api.Controller{
		Service: service,
	}
	apiAPI := &api.API{
		Inject:     inject,
		Hertz:      hertz,
		Controller: controller,
		Service:    service,
	}
	return apiAPI, nil
}
