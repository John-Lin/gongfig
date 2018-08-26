package actions

import (
	"fmt"
	"bufio"
	"os"
	"net/http"
	"github.com/mitchellh/mapstructure"
	"strings"
	"log"
	"time"
)

func flushAll(adminURL string) {
	client := &http.Client{Timeout: Timeout * time.Second}

	// We obtain resources data concurrently and push them to the channel that
	// will be handled by services and routes deleting logic
	flushData := make(chan *resourceAnswer)

	// Collect representation of all resources
	for _, resource := range Apis {
		fullPath := getFullPath(adminURL, resource)

		go getResourceList(client, flushData, fullPath, resource)

	}

	resourcesNum := len(Apis)
	config := map[string]Data{}

	for {
		resource := <- flushData
		config[resource.resourceName] = resource.config

		resourcesNum--

		if resourcesNum == 0 {
			flushResources(client, adminURL, config)
			fmt.Println("Done")
			break
		}
	}
}

func flushResources(client *http.Client, url string, config map[string]Data) {
	// Firstly we need delete routes and only then services,
	// as routes are nested resources of services
	for _, resourceType := range []string{RoutesKey, ServicesKey} {
		// In order to not overload the kong, limit concurrent post requests to 10
		reqLimitChan := make(chan bool, 10)

		for _, item := range config[resourceType] {
			reqLimitChan <- true

			// Convert item to resource object for further deleting it from Kong
			var instance ResourceInstance
			mapstructure.Decode(item, &instance)

			go func(instance ResourceInstance){
				defer func() { <-reqLimitChan}()

				// Compose path to routes
				instancePathElements := []string{resourceType, instance.Id}
				instancePath := strings.Join(instancePathElements, "/")
				instanceURL := getFullPath(url, instancePath)

				request, _ := http.NewRequest(http.MethodDelete, instanceURL, nil)

				response, err := client.Do(request)

				if err != nil {
					log.Fatal("Request to Kong admin failed")
					os.Exit(1)
				}

				if response.StatusCode != 204 {
					log.Fatal("Was not able to Delete item ", instance.Id)
					os.Exit(1)
				}

			}(instance)
		}

		// Wait till all routes deleting is finished
		for i := 0; i < cap(reqLimitChan); i++ {
			reqLimitChan <- true
		}
	}
}

// Flush - main function that is called by CLI in wipe Kong config
func Flush(adminURL string) {
	fmt.Println("All services and routes will be deleted from kong, are you sure? Write yes or no:")
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')

	// Delete \n at the end
	answer = answer[0:len(answer)-1]

	if answer== "yes" {
		flushAll(adminURL)
	} else {
		fmt.Println("Configuration was not flushed")
	}
}