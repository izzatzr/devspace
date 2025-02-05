package cloud

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/devspace-cloud/devspace/pkg/devspace/config/configutil"
	"github.com/devspace-cloud/devspace/pkg/devspace/config/versions/latest"
	"github.com/devspace-cloud/devspace/pkg/util/log"
	"github.com/devspace-cloud/devspace/pkg/util/survey"
	"github.com/mgutz/ansi"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// IngressName is the ingress name to create
const IngressName = "devspace-ingress"

// CreateIngress creates an ingress in the space if there is none
func (p *Provider) CreateIngress(config *latest.Config, client kubernetes.Interface, space *Space, host string) error {
	namespace, err := configutil.GetDefaultNamespace(config)
	if err != nil {
		return errors.Wrap(err, "get default namespace")
	}

	// Let user select service
	serviceNameList := []string{}

	serviceList, err := client.CoreV1().Services(namespace).List(metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "list services")
	}

	for _, service := range serviceList.Items {
		// We skip tiller-deploy, because usually you don't want to create an ingress for tiller
		if service.Name == "tiller-deploy" {
			continue
		}

		if service.Spec.Type == v1.ServiceTypeClusterIP {
			if service.Spec.ClusterIP == "None" {
				continue
			}

			for _, port := range service.Spec.Ports {
				serviceNameList = append(serviceNameList, service.Name+":"+strconv.Itoa(int(port.Port)))
			}
		}
	}

	serviceName := ""
	servicePort := ""

	if len(serviceNameList) == 0 {
		return fmt.Errorf("Couldn't find any active services an ingress could connect to. Please make sure you have a service for your application")
	} else if len(serviceNameList) == 1 {
		splitted := strings.Split(serviceNameList[0], ":")

		serviceName = splitted[0]
		servicePort = splitted[1]
	} else {
		// Ask user which service
		splitted := strings.Split(survey.Question(&survey.QuestionOptions{
			Question: fmt.Sprintf("Please specify the service you want to connect '%s' to", ansi.Color(host, "white+b")),
			Options:  serviceNameList,
		}), ":")

		serviceName = splitted[0]
		servicePort = splitted[1]
	}

	// Get the cluster key
	key, err := p.GetClusterKey(space.Cluster)
	if err != nil {
		return errors.Wrap(err, "get cluster key")
	}

	// Response struct
	response := struct {
		ManagerCreateIngressPath bool `json:"manager_createKubeContextDomainIngressPath"`
	}{}

	// Do the request
	err = p.GrapqhlRequest(`
		mutation($spaceID: Int!, $ingressName: String!, $host: String!, $newPath: String!, $newServiceName: String!, $newServicePort: String!, $key: String) {
			manager_createKubeContextDomainIngressPath(
				spaceID: $spaceID,
				key: $key,
				ingressName: $ingressName,
				host: $host,
				newPath: $newPath,
				newServiceName: $newServiceName,
				newServicePort: $newServicePort,
			)
		}
	`, map[string]interface{}{
		"key":            key,
		"spaceID":        space.SpaceID,
		"ingressName":    IngressName,
		"host":           host,
		"newPath":        "",
		"newServiceName": serviceName,
		"newServicePort": servicePort,
	}, &response)
	if err != nil {
		return errors.Wrap(err, "graphql create ingress path")
	}

	// Check result
	if response.ManagerCreateIngressPath == false {
		return errors.New("Mutation returned wrong result")
	}

	log.Infof("Successfully created ingress in space %s", space.Name)
	return nil
}
