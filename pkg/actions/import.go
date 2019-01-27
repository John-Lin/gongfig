package actions

import (
	"os"
	"log"
	"encoding/json"
	"net/http"
	"time"
	"fmt"
	"github.com/mitchellh/mapstructure"
	"sync"
)

type ConcurrentStringMap struct {
	sync.Mutex
	store map[string]string
}

func (concurrentStringMap *ConcurrentStringMap) Add(key, value string) {
	concurrentStringMap.Lock()
	defer concurrentStringMap.Unlock()

	concurrentStringMap.store[key] = value
}

func createEntries(client *http.Client, adminURL string, configMap map[string][]interface{}) {
	// In order to not overload the server, limit concurrent post requests to 10
	reqLimitChan := make(chan bool, 10)
	servicesConnectionBundle := ConnectionBundle{client, adminURL, reqLimitChan}

	// Map local resource ids with newly created
	concurrentStringMap := ConcurrentStringMap{store: make(map[string]string)}

	// Create services and routes in separate cycle as they depend on each other
	// and services should be created before routes
	for _, item := range configMap[ServicesPath] {
		reqLimitChan <- true

		// Convert item to service object for further creating it at Kong
		var service Service
		mapstructure.Decode(item, &service)

		go createServiceWithRoutes(&servicesConnectionBundle, service, &concurrentStringMap)
	}

	// Create upstreams and targets in separate cycle as they also depend on each other
	// (as services and routes)
	upstreamsConnectionBundle := ConnectionBundle{client, adminURL, reqLimitChan}

	for _, item := range configMap[UpstreamsPath] {
		reqLimitChan <- true

		var upstream Upstream
		mapstructure.Decode(item, &upstream)

		go createUpstreamsWithTargets(&upstreamsConnectionBundle, upstream)
	}


	url := getFullPath(adminURL, []string{CertificatesPath})

	for _, item := range configMap[CertificatesPath] {
		reqLimitChan <- true

		var certificate Certificate
		mapstructure.Decode(item, &certificate)

		go addResource(
			&ConnectionBundle{client, url, reqLimitChan},
			certificate, &concurrentStringMap)
	}

	url = getFullPath(adminURL, []string{ConsumersPath})

	for _, item := range configMap[ConsumersPath] {
		reqLimitChan <- true

		var consumer Consumer
		mapstructure.Decode(item, &consumer)

		bundle := &ConnectionBundle{client, url, reqLimitChan}

		go createConsumersWithKeyAuths(bundle, consumer, &concurrentStringMap)
	}

	// Be aware all left requests are finished prior creation of depending resources
	for i := 0; i < cap(reqLimitChan); i++ {
		reqLimitChan <- true
	}

	// Clean channel for further creation
	for i := 0; i < cap(reqLimitChan); i++ {
		<- reqLimitChan
	}

	pluginsURL := getFullPath(adminURL, []string{PluginsPath})

	//Create plugins
	for _, item := range configMap[PluginsPath] {
		reqLimitChan <- true

		var plugin Plugin
		mapstructure.Decode(item, &plugin)

		if plugin.ServiceId != "" {
			plugin.ServiceId = concurrentStringMap.store[plugin.ServiceId]
		}

		if plugin.RouteId != "" {
			plugin.RouteId = concurrentStringMap.store[plugin.RouteId]
		}

		if plugin.ConsumerId != "" {
			plugin.ConsumerId = concurrentStringMap.store[plugin.ConsumerId]
		}

		go addResource(
			&ConnectionBundle{client, pluginsURL, reqLimitChan},
			&plugin, &concurrentStringMap)
	}

	// Be aware all requests are finished prior to program exit
	for i := 0; i < cap(reqLimitChan); i++ {
		reqLimitChan <- true
	}

}

func createConsumersWithKeyAuths(requestBundle *ConnectionBundle, consumer Consumer, idMap *ConcurrentStringMap) {
	addResource(
		&ConnectionBundle{requestBundle.Client, requestBundle.URL, requestBundle.ReqLimitChan},
		consumer, idMap)

}

func createServiceWithRoutes(requestBundle *ConnectionBundle, service Service, idMap *ConcurrentStringMap) {
	defer func() { <-requestBundle.ReqLimitChan}()

	// Get path to the services collection
	servicesURL := getFullPath(requestBundle.URL, []string{ServicesPath})

	// Clear routes field as it is created in separate request
	routes := service.Routes
	service.Routes = nil

	// Record and clear id as it is for internal purposes
	id := service.Id
	service.Id = ""


	// Create services first, as routes are nested resources
	serviceExternalId, err := requestNewResource(requestBundle.Client, service, servicesURL)

	if err != nil {
		log.Fatalf("Failed to create service, %v\n", err)
	}

	idMap.Add(id, serviceExternalId)

	// Compose path to routes
	routesPathElements := []string{ServicesPath, service.Name, RoutesPath}
	routesURL := getFullPath(requestBundle.URL, routesPathElements)

	// Create routes one by one
	for _, route := range routes {
		// Record and clear id as it is for internal purposes
		id := route.Id
		route.Id = ""

		routeExternalId, err := requestNewResource(requestBundle.Client, route, routesURL)

		if err != nil {
			log.Fatalf("Could not create new resource, %v\n", err)
		}

		idMap.Add(id, routeExternalId)
	}

}

func createUpstreamsWithTargets(requestBundle *ConnectionBundle, upstream Upstream) {
	defer func() { <-requestBundle.ReqLimitChan}()

	// Clear routes field as it is created in separate request
	targets := upstream.Targets
	upstream.Targets = nil

	// Clear id
	upstream.Id = ""

	upstreamsURL := getFullPath(requestBundle.URL, []string{UpstreamsPath})
	_, err := requestNewResource(requestBundle.Client, upstream, upstreamsURL)

	if err != nil {
		log.Fatalf("Could not create new resource, %v\n", err)
	}

	targetsURL := getFullPath(requestBundle.URL, []string{UpstreamsPath, upstream.Name, TargetsPath})

	for _, target := range targets {
		_, err := requestNewResource(requestBundle.Client, target, targetsURL)

		if err != nil {
			log.Fatalf("Failed to create target, %v\n", err)
		}
	}

}

// Import - main function that is called by CLI in order to create resources at Kong service
func Import(adminURL string, filePath string) {
	client := &http.Client{Timeout: Timeout * time.Second}

	configFile, err := os.OpenFile(filePath, os.O_RDONLY,0444)

	if err != nil {
		log.Fatalf("Failed to read config file. %v\n", err.Error())
	}

	jsonParser := json.NewDecoder(configFile)
	var configMap = make(map[string][]interface{})

	if err :=  jsonParser.Decode(&configMap); err != nil {
		log.Fatalf("Failed to parse json file. %v\n", err)
	}

	createEntries(client, adminURL, configMap)

	fmt.Println("Done")
}