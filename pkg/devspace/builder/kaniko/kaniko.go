package kaniko

import (
	"io"
	"strings"

	"github.com/devspace-cloud/devspace/pkg/devspace/builder/helper"
	"github.com/devspace-cloud/devspace/pkg/devspace/config/configutil"
	"github.com/devspace-cloud/devspace/pkg/devspace/config/generated"
	"github.com/devspace-cloud/devspace/pkg/devspace/config/versions/latest"
	"github.com/devspace-cloud/devspace/pkg/devspace/docker"
	"github.com/devspace-cloud/devspace/pkg/devspace/kubectl"
	"github.com/devspace-cloud/devspace/pkg/devspace/registry"
	"github.com/devspace-cloud/devspace/pkg/devspace/services"
	"github.com/devspace-cloud/devspace/pkg/devspace/services/targetselector"
	logpkg "github.com/devspace-cloud/devspace/pkg/util/log"
	"github.com/devspace-cloud/devspace/pkg/util/randutil"

	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/cli/cli/command/image/build"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	dockerterm "github.com/docker/docker/pkg/term"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/util/interrupt"
)

// EngineName is the name of the building engine
const EngineName = "kaniko"

var (
	_, stdout, stderr = dockerterm.StdStreams()
)

// Builder holds the necessary information to build and push docker images
type Builder struct {
	helper *helper.BuildHelper

	PullSecretName string
	FullImageName  string
	BuildNamespace string

	allowInsecureRegistry bool
	kubectl               kubernetes.Interface
	dockerClient          client.CommonAPIClient
}

// Wait timeout is the maximum time to wait for the kaniko init and build container to get ready
const waitTimeout = 2 * time.Minute

// NewBuilder creates a new kaniko.Builder instance
func NewBuilder(config *latest.Config, dockerClient client.CommonAPIClient, kubectl kubernetes.Interface, imageConfigName string, imageConf *latest.ImageConfig, imageTag string, isDev bool, log logpkg.Logger) (*Builder, error) {
	buildNamespace, err := configutil.GetDefaultNamespace(config)
	if err != nil {
		return nil, errors.New("Error retrieving default namespace")
	}

	if imageConf.Build.Kaniko.Namespace != nil && *imageConf.Build.Kaniko.Namespace != "" {
		buildNamespace = *imageConf.Build.Kaniko.Namespace
	}

	allowInsecurePush := false
	if imageConf.Build.Kaniko.Insecure != nil {
		allowInsecurePush = *imageConf.Build.Kaniko.Insecure
	}

	pullSecretName := ""
	if imageConf.Build.Kaniko.PullSecret != nil {
		pullSecretName = *imageConf.Build.Kaniko.PullSecret
	}

	builder := &Builder{
		PullSecretName: pullSecretName,
		FullImageName:  *imageConf.Image + ":" + imageTag,
		BuildNamespace: buildNamespace,

		allowInsecureRegistry: allowInsecurePush,

		kubectl:      kubectl,
		dockerClient: dockerClient,
		helper:       helper.NewBuildHelper(config, EngineName, imageConfigName, imageConf, imageTag, isDev),
	}

	// create pull secret
	err = builder.createPullSecret(log)
	if err != nil {
		return nil, errors.Wrap(err, "create pull secret")
	}

	return builder, nil
}

// Build implements the interface
func (b *Builder) Build(log logpkg.Logger) error {
	return b.helper.Build(b, log)
}

// ShouldRebuild determines if an image has to be rebuilt
func (b *Builder) ShouldRebuild(cache *generated.CacheConfig) (bool, error) {
	return b.helper.ShouldRebuild(cache)
}

// Authenticate authenticates kaniko for pushing to the RegistryURL (if username == "", it will try to get login data from local docker daemon)
func (b *Builder) createPullSecret(log logpkg.Logger) error {
	username, password := "", ""

	if b.PullSecretName != "" {
		return nil
	}

	registryURL, err := registry.GetRegistryFromImageName(b.FullImageName)
	if err != nil {
		return err
	}

	email := "noreply@devspace.cloud"
	authConfig, err := docker.GetAuthConfig(b.dockerClient, registryURL, true)
	if err != nil {
		return err
	}

	username = authConfig.Username
	email = authConfig.Email

	if authConfig.Password != "" {
		password = authConfig.Password
	} else {
		password = authConfig.IdentityToken
	}

	return registry.CreatePullSecret(b.kubectl, b.BuildNamespace, registryURL, username, password, email, log)
}

// BuildImage builds a dockerimage within a kaniko pod
func (b *Builder) BuildImage(contextPath, dockerfilePath string, entrypoint *[]*string, log logpkg.Logger) error {
	// Check if we should overwrite entrypoint
	if entrypoint != nil && len(*entrypoint) > 0 {
		dockerfilePath, err := helper.CreateTempDockerfile(dockerfilePath, *entrypoint)
		if err != nil {
			return err
		}

		defer os.RemoveAll(filepath.Dir(dockerfilePath))
	}

	// Buildoptions
	options := &types.ImageBuildOptions{}
	if b.helper.ImageConf.Build != nil && b.helper.ImageConf.Build.Kaniko != nil && b.helper.ImageConf.Build.Kaniko.Options != nil {
		if b.helper.ImageConf.Build.Kaniko.Options.BuildArgs != nil {
			options.BuildArgs = *b.helper.ImageConf.Build.Kaniko.Options.BuildArgs
		}
		if b.helper.ImageConf.Build.Kaniko.Options.Target != nil {
			options.Target = *b.helper.ImageConf.Build.Kaniko.Options.Target
		}
		if b.helper.ImageConf.Build.Kaniko.Options.Network != nil {
			options.NetworkMode = *b.helper.ImageConf.Build.Kaniko.Options.Network
		}
	}

	// Generate the build pod spec
	randString, _ := randutil.GenerateRandomString(12)
	buildID := strings.ToLower(randString)
	buildPod, err := b.getBuildPod(buildID, options, dockerfilePath)
	if err != nil {
		return errors.Wrap(err, "get build pod")
	}

	// Delete the build pod when we are done or get interrupted during build
	deleteBuildPod := func() {
		gracePeriod := int64(3)
		deleteErr := b.kubectl.CoreV1().Pods(b.BuildNamespace).Delete(buildPod.Name, &metav1.DeleteOptions{
			GracePeriodSeconds: &gracePeriod,
		})

		if deleteErr != nil {
			log.Errorf("Failed to delete build pod: %s", deleteErr.Error())
		}
	}

	intr := interrupt.New(nil, deleteBuildPod)
	err = intr.Run(func() error {
		defer log.StopWait()

		buildPodCreated, err := b.kubectl.CoreV1().Pods(b.BuildNamespace).Create(buildPod)
		if err != nil {
			return fmt.Errorf("Unable to create build pod: %s", err.Error())
		}

		now := time.Now()
		log.StartWait("Waiting for build init container to start")

		for {
			buildPod, _ = b.kubectl.CoreV1().Pods(b.BuildNamespace).Get(buildPodCreated.Name, metav1.GetOptions{})
			if len(buildPod.Status.InitContainerStatuses) > 0 && buildPod.Status.InitContainerStatuses[0].State.Running != nil {
				break
			}

			time.Sleep(5 * time.Second)
			if time.Since(now) >= waitTimeout {
				return fmt.Errorf("Timeout waiting for init container")
			}
		}

		// Get rest config
		restConfig, err := kubectl.GetRestConfig(b.helper.Config)
		if err != nil {
			return errors.Wrap(err, "get rest config")
		}

		// Get ignore rules from docker ignore
		ignoreRules, err := build.ReadDockerignore(contextPath)
		if err != nil {
			return err
		}

		ignoreRules = append(ignoreRules, ".devspace/")
		log.StartWait("Uploading files to build container")

		// Copy complete context
		err = kubectl.Copy(restConfig, buildPod, buildPod.Spec.InitContainers[0].Name, kanikoContextPath, contextPath, ignoreRules)
		if err != nil {
			return fmt.Errorf("Error uploading files to container: %v", err)
		}

		// Copy dockerfile
		err = kubectl.Copy(restConfig, buildPod, buildPod.Spec.InitContainers[0].Name, kanikoContextPath, dockerfilePath, ignoreRules)
		if err != nil {
			return fmt.Errorf("Error uploading files to container: %v", err)
		}

		// Tell init container we are done
		_, _, err = kubectl.ExecBuffered(restConfig, buildPod, buildPod.Spec.InitContainers[0].Name, []string{"touch", doneFile}, nil)
		if err != nil {
			return fmt.Errorf("Error executing command in init container: %v", err)
		}

		log.Done("Uploaded files to container")
		log.StartWait("Waiting for kaniko container to start")

		now = time.Now()
		for true {
			buildPod, _ = b.kubectl.CoreV1().Pods(b.BuildNamespace).Get(buildPodCreated.Name, metav1.GetOptions{})
			if len(buildPod.Status.ContainerStatuses) > 0 && buildPod.Status.ContainerStatuses[0].Ready {
				break
			}

			time.Sleep(2 * time.Second)
			if time.Since(now) >= waitTimeout {
				return fmt.Errorf("Timeout waiting for kaniko build pod")
			}
		}

		log.StopWait()
		log.Done("Build pod has started")

		// Determine output writer
		var writer io.Writer
		if log == logpkg.GetInstance() {
			writer = stdout
		} else {
			writer = log
		}

		stdoutLogger := kanikoLogger{out: writer}
		stderrLogger := kanikoLogger{out: writer}

		// Stream the logs
		err = services.StartLogsWithWriter(b.helper.Config, b.kubectl, targetselector.CmdParameter{PodName: &buildPod.Name, ContainerName: &buildPod.Spec.Containers[0].Name, Namespace: &buildPod.Namespace}, true, 100, log, stdoutLogger, stderrLogger)
		if err != nil {
			return fmt.Errorf("Error during printling build logs: %v", err)
		}

		log.StartWait("Checking build status")
		for true {
			time.Sleep(time.Second)

			// Check if build was successfull
			pod, err := b.kubectl.CoreV1().Pods(b.BuildNamespace).Get(buildPodCreated.Name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("Error checking if build was successful: %v", err)
			}

			// Check if terminated
			if pod.Status.ContainerStatuses[0].State.Terminated != nil {
				if pod.Status.ContainerStatuses[0].State.Terminated.ExitCode != 0 {
					return fmt.Errorf("Error building image (Exit Code %d)", pod.Status.ContainerStatuses[0].State.Terminated.ExitCode)
				}

				break
			}
		}
		log.StopWait()

		log.Done("Done building image")
		return nil
	})

	if err != nil {
		// Delete all build pods on error
		pods, getErr := b.kubectl.CoreV1().Pods(b.BuildNamespace).List(metav1.ListOptions{
			LabelSelector: "devspace-build=true",
		})
		if getErr != nil {
			return err
		}
		for _, pod := range pods.Items {
			b.kubectl.CoreV1().Pods(b.BuildNamespace).Delete(pod.Name, &metav1.DeleteOptions{})
		}

		return err
	}

	return nil
}
