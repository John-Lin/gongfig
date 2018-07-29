package actions

import (
	"net/http"
	"time"
	"encoding/json"
	"io/ioutil"
	"fmt"

	"github.com/mitchellh/mapstructure"
	"sort"
)

type Data []interface{}

// All items are contained of data property of json answer
type resourceConfig struct {
	Data Data `json:"data"`
}

// resourceAnswer contains resource name and its configuration so
// file writer can compose json with name as a key and complete resource configuration as a value
type resourceAnswer struct {
	resourceName string
	config Data
}

// Prepare config for writing: put routes as nested resources of services, omit unnecessary field etc
func composeConfig(config map[string]Data) map[string]interface{} {
	preparedConfig := make(map[string]interface{})
	serviceMap := make(map[string]*Service)

	// Create a map of services where key is service id in order to effectively
	// search services for pasting there corresponding routes
	for _, item := range config[ServicesKey] {
		var service Service
		mapstructure.Decode(item, &service)
		serviceMap[service.Id] = &service
	}

	// Add routes to services as nested files so futher it will be written to a file
	for _, item := range config[RoutesKey] {
		var route Route
		mapstructure.Decode(item, &route)

		var routePrepared RoutePrepared
		mapstructure.Decode(item, &routePrepared)

		serviceMap[route.Service.Id].Routes = append(serviceMap[route.Service.Id].Routes, routePrepared)
	}

	var services []ServicePrepared

	// Rework serviceMap to a slice for writing it to the config file
	// as service entity already has an id field and it does not need to duplicate it
	for _, service := range serviceMap{
		servicePrepared := ServicePrepared{
			service.Name,
			service.Host,
			service.Path,
			service.Port,
			service.Protocol,
			service.ConnectTimeout,
			service.ReadTimeout,
			service.WriteTimeout,
			service.Routes,
		}

		services = append(services, servicePrepared)
	}

	//Sort services by name
	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})

	preparedConfig[ServicesKey] = services

	return preparedConfig
}

func Export(adminUrl string, filePath string) {
	client := &http.Client{Timeout: 10 * time.Second}

	// We obtain resources data concurrently and push them to the channel that
	// will be handled by file writer
	writeData := make(chan *resourceAnswer)

	// Collect representation of all resources
	for _, resource := range Apis {
		fullPath := getFullPath(adminUrl, resource)

		go getResourceList(client, writeData, fullPath, resource)

	}

	resourcesNum := len(Apis)
	config := map[string]Data{}
	var preparedConfig map[string]interface{}

	// Before writing to a file the program composes json
	// It waits to obtain from channel exactly the same amount as number of resources
	// After that it composes the data in proper format, writes to a file and closes
	for {
		resource := <- writeData
		config[resource.resourceName] = resource.config

		resourcesNum--

		// resourcesNum is 0 means all needed resources are collected
		// and we can prepare config for writing it to a file
		if resourcesNum == 0 {
			preparedConfig = composeConfig(config)
			break
		}
	}

	jsonAnswer, _ := json.MarshalIndent(preparedConfig, "", "    ")
	ioutil.WriteFile(filePath, jsonAnswer, 0644)
	fmt.Println("Done")
}