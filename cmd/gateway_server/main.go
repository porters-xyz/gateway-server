package main

import (
	"fmt"
	"github.com/fasthttp/router"
	fasthttpprometheus "github.com/flf2ko/fasthttp-prometheus"
	"github.com/jellydator/ttlcache/v3"
	"github.com/valyala/fasthttp"
	"pokt_gateway_server/cmd/gateway_server/internal/config"
	"pokt_gateway_server/cmd/gateway_server/internal/controllers"
	"pokt_gateway_server/cmd/gateway_server/internal/middleware"
	"pokt_gateway_server/internal/db_query"
	"pokt_gateway_server/internal/logging"
	"pokt_gateway_server/internal/pokt_apps_registry"
	"pokt_gateway_server/internal/pokt_client_decorators"
	"pokt_gateway_server/pkg/pokt/pokt_v0"
	"pokt_gateway_server/pkg/pokt/pokt_v0/models"
)

const (
	maxDbConns = 50
)

func main() {
	// Initialize configuration provider from environment variables
	gatewayConfigProvider := config.NewDotEnvConfigProvider()

	// Initialize logger using the configured settings
	logger, err := logging.NewLogger(gatewayConfigProvider)
	if err != nil {
		// If logger initialization fails, panic with the error
		panic(err)
	}

	querier, pool, err := db_query.InitDB(logger, gatewayConfigProvider, maxDbConns)
	if err != nil {
		logger.Sugar().Fatal(err)
		return
	}

	// Close connection to pool afterward
	defer pool.Close()

	// Initialize a POKT client using the configured POKT RPC host and timeout
	client, err := pokt_v0.NewBasicClient(gatewayConfigProvider.GetPoktRPCFullHost(), gatewayConfigProvider.GetPoktRPCTimeout())
	if err != nil {
		// If POKT client initialization fails, log the error and exit
		logger.Sugar().Fatal(err)
		return
	}

	// Initialize a TTL cache for session caching
	sessionCache := ttlcache.New[string, *models.GetSessionResponse](
		ttlcache.WithTTL[string, *models.GetSessionResponse](gatewayConfigProvider.GetSessionCacheTTL()), //@todo: make this configurable via env ?
	)

	poktApplicationRegistry := pokt_apps_registry.NewCachedRegistry(client, querier, gatewayConfigProvider, logger.Named("pokt_application_registry"))

	cachedPoktClient := pokt_client_decorators.NewCachedClient(client, sessionCache)
	// Define routers
	r := router.New()

	// Create a relay controller with the necessary dependencies (logger, registry, cached relayer)
	relayController := controllers.NewRelayController(cachedPoktClient, poktApplicationRegistry, logger.Named("relay_controller"))

	relayRouter := r.Group("/relay")
	relayRouter.POST("/{catchAll:*}", relayController.HandleRelay)

	poktAppsController := controllers.NewPoktAppsController(poktApplicationRegistry, querier, gatewayConfigProvider, logger.Named("pokt_apps_controller"))
	poktAppsRouter := r.Group("/poktapps")

	poktAppsRouter.GET("/", middleware.XAPIKeyAuth(poktAppsController.GetAll, gatewayConfigProvider))
	poktAppsRouter.POST("/", middleware.XAPIKeyAuth(poktAppsController.AddApplication, gatewayConfigProvider))
	poktAppsRouter.DELETE("/{app_id}", middleware.XAPIKeyAuth(poktAppsController.DeleteApplication, gatewayConfigProvider))

	// Add Middleware for Generic E2E Prom Tracking
	p := fasthttpprometheus.NewPrometheus("fasthttp")
	fastpHandler := p.WrapHandler(r)

	logger.Info("Gateway Server Started")
	// Start the fasthttp server and listen on the configured server port
	if err := fasthttp.ListenAndServe(fmt.Sprintf(":%d", gatewayConfigProvider.GetHTTPServerPort()), fastpHandler); err != nil {
		// If an error occurs during server startup, log the error and exit
		logger.Sugar().Fatalw("Error in ListenAndServe", "err", err)
	}
}
