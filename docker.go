package main
import (
	"github.com/fsouza/go-dockerclient"
	"github.com/leekchan/gtf"
	"bytes"
	"github.com/BurntSushi/toml"
	"text/template"
	"strings"
	"github.com/BurntSushi/ty/fun"
	"strconv"
)

type DockerProvider struct {
	Watch        bool
	Endpoint     string
	dockerClient *docker.Client
	Filename     string
	Domain       string
}

var DockerFuncMap = template.FuncMap{
	"getBackend": func(container docker.Container) string {
		for key, value := range container.Config.Labels {
			if (key == "traefik.backend") {
				return value
			}
		}
		return getHost(container)
	},
	"getPort": func(container docker.Container) string {
		for key, value := range container.Config.Labels {
			if (key == "traefik.port") {
				return value
			}
		}
		for key, _ := range container.NetworkSettings.Ports {
			return key.Port()
		}
		return ""
	},
	"getWeight": func(container docker.Container) string {
		for key, value := range container.Config.Labels {
			if (key == "traefik.weight") {
				return value
			}
		}
		return "0"
	},
	"replace": func(s1 string, s2 string, s3 string) string {
		return strings.Replace(s3, s1, s2, -1)
	},
	"getHost": getHost,
}

func (provider *DockerProvider) Provide(configurationChan chan <- *Configuration) {
	if client, err := docker.NewClient(provider.Endpoint); err != nil {
		log.Fatalf("Failed to create a client for docker, error: %s", err)
	} else {
		provider.dockerClient = client
		dockerEvents := make(chan *docker.APIEvents)
		if (provider.Watch) {
			provider.dockerClient.AddEventListener(dockerEvents)
			go func() {
				for {
					event := <-dockerEvents
					if(event.Status == "start" || event.Status == "die"){
						log.Debug("Docker event receveived %+v", event)
						configuration := provider.loadDockerConfig()
						if (configuration != nil) {
							configurationChan <- configuration
						}
					}
				}
			}()
		}

		configuration := provider.loadDockerConfig()
		configurationChan <- configuration
	}
}

func (provider *DockerProvider) loadDockerConfig() *Configuration {
	configuration := new(Configuration)
	containerList, _ := provider.dockerClient.ListContainers(docker.ListContainersOptions{})
	containersInspected := []docker.Container{}
	hosts := map[string][]docker.Container{}

	// get inspect containers
	for _, container := range containerList {
		containerInspected, _ := provider.dockerClient.InspectContainer(container.ID)
		containersInspected = append(containersInspected, *containerInspected)
	}

	// filter containers
	filteredContainers := fun.Filter(func(container docker.Container) bool {
		if (len(container.NetworkSettings.Ports) == 0) {
			log.Debug("Filtering container without port %s", container.Name)
			return false
		}
		_, err := strconv.Atoi(container.Config.Labels["traefik.port"])
		if (len(container.NetworkSettings.Ports) > 1 &&  err != nil) {
			log.Debug("Filtering container with more than 1 port and no traefik.port label %s", container.Name)
			return false
		}
		if (container.Config.Labels["traefik.enable"] == "false") {
			log.Debug("Filtering disabled container %s", container.Name)
			return false
		}
		return true
	}, containersInspected).([]docker.Container)

	for _, container := range filteredContainers {
		hosts[getHost(container)] = append(hosts[getHost(container)], container)
	}

	templateObjects := struct {
		Containers []docker.Container
		Hosts      map[string][]docker.Container
		Domain     string
	}{
		filteredContainers,
		hosts,
		provider.Domain,
	}
	gtf.Inject(DockerFuncMap)
	buf, err := Asset("providerTemplates/docker.tmpl")
	if err != nil {
		log.Error("Error reading file", err)
	}
	tmpl, err := template.New(provider.Filename).Funcs(DockerFuncMap).Parse(string(buf))
	if err != nil {
		log.Error("Error reading file", err)
		return nil
	}

	var buffer bytes.Buffer

	err = tmpl.Execute(&buffer, templateObjects)
	if err != nil {
		log.Error("Error with docker template", err)
		return nil
	}

	if _, err := toml.Decode(buffer.String(), configuration); err != nil {
		log.Error("Error creating docker configuration", err)
		return nil
	}
	return configuration
}

func getHost(container docker.Container) string {
	for key, value := range container.Config.Labels {
		if (key == "traefik.host") {
			return value
		}
	}
	return strings.Replace(strings.Replace(container.Name, "/", "", -1), ".", "-", -1)
}