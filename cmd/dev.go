package cmd

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/devspace-cloud/devspace/pkg/devspace/build"
	"github.com/devspace-cloud/devspace/pkg/devspace/cloud"
	"github.com/devspace-cloud/devspace/pkg/devspace/dependency"
	deploy "github.com/devspace-cloud/devspace/pkg/devspace/deploy/util"
	"github.com/devspace-cloud/devspace/pkg/devspace/services/targetselector"
	"github.com/devspace-cloud/devspace/pkg/devspace/watch"
	"github.com/mgutz/ansi"

	"github.com/devspace-cloud/devspace/pkg/devspace/config/configutil"
	"github.com/devspace-cloud/devspace/pkg/devspace/config/generated"
	latest "github.com/devspace-cloud/devspace/pkg/devspace/config/versions/latest"
	v1 "github.com/devspace-cloud/devspace/pkg/devspace/config/versions/latest"
	"github.com/devspace-cloud/devspace/pkg/devspace/docker"
	"github.com/devspace-cloud/devspace/pkg/devspace/kubectl"
	"github.com/devspace-cloud/devspace/pkg/devspace/registry"
	"github.com/devspace-cloud/devspace/pkg/devspace/services"
	"github.com/devspace-cloud/devspace/pkg/util/log"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

// DevCmd is a struct that defines a command call for "up"
type DevCmd struct {
	SkipPush                bool
	AllowCyclicDependencies bool

	ForceBuild        bool
	BuildSequential   bool
	ForceDeploy       bool
	Deployments       string
	ForceDependencies bool

	Sync            bool
	Terminal        bool
	ExitAfterDeploy bool
	SkipPipeline    bool
	SwitchContext   bool
	Portforwarding  bool
	VerboseSync     bool
	Selector        string
	Container       string
	LabelSelector   string
	Namespace       string
}

// NewDevCmd creates a new devspace dev command
func NewDevCmd() *cobra.Command {
	cmd := &DevCmd{}

	devCmd := &cobra.Command{
		Use:   "dev",
		Short: "Starts the development mode",
		Long: `
#######################################################
################### devspace dev ######################
#######################################################
Starts your project in development mode:
1. Builds your Docker images and override entrypoints if specified
2. Deploys the deployments via helm or kubectl
3. Forwards container ports to the local computer
4. Starts the sync client
5. Enters the container shell
#######################################################`,
		Run: cmd.Run,
	}

	devCmd.Flags().BoolVar(&cmd.AllowCyclicDependencies, "allow-cyclic", false, "When enabled allows cyclic dependencies")

	devCmd.Flags().BoolVarP(&cmd.ForceBuild, "force-build", "b", false, "Forces to build every image")
	devCmd.Flags().BoolVar(&cmd.BuildSequential, "build-sequential", false, "Builds the images one after another instead of in parallel")

	devCmd.Flags().BoolVarP(&cmd.ForceDeploy, "force-deploy", "d", false, "Forces to deploy every deployment")
	devCmd.Flags().StringVar(&cmd.Deployments, "deployments", "", "Only deploy a specifc deployment (You can specify multiple deployments comma-separated")
	devCmd.Flags().BoolVar(&cmd.ForceDependencies, "force-dependencies", false, "Forces to re-evaluate dependencies (use with --force-build --force-deploy to actually force building & deployment of dependencies)")

	devCmd.Flags().BoolVarP(&cmd.SkipPipeline, "skip-pipeline", "x", false, "Skips build & deployment and only starts sync, portforwarding & terminal")
	devCmd.Flags().BoolVar(&cmd.SkipPush, "skip-push", false, "Skips image pushing, useful for minikube deployment")

	devCmd.Flags().BoolVar(&cmd.Sync, "sync", true, "Enable code synchronization")
	devCmd.Flags().BoolVar(&cmd.VerboseSync, "verbose-sync", false, "When enabled the sync will log every file change")

	devCmd.Flags().BoolVar(&cmd.Portforwarding, "portforwarding", true, "Enable port forwarding")

	devCmd.Flags().BoolVar(&cmd.Terminal, "terminal", true, "Enable terminal (true or false)")
	devCmd.Flags().StringVarP(&cmd.Selector, "selector", "s", "", "Selector name (in config) to select pods/container for terminal")
	devCmd.Flags().StringVarP(&cmd.Container, "container", "c", "", "Container name where to open the shell")
	devCmd.Flags().StringVarP(&cmd.LabelSelector, "label-selector", "l", "", "Comma separated key=value selector list to use for terminal (e.g. release=test)")

	devCmd.Flags().StringVarP(&cmd.Namespace, "namespace", "n", "", "The namespace to deploy to")

	devCmd.Flags().BoolVar(&cmd.SwitchContext, "switch-context", false, "Switch kubectl context to the DevSpace context")
	devCmd.Flags().BoolVar(&cmd.ExitAfterDeploy, "exit-after-deploy", false, "Exits the command after building the images and deploying the project")

	return devCmd
}

// Run executes the command logic
func (cmd *DevCmd) Run(cobraCmd *cobra.Command, args []string) {
	// Set config root
	configExists, err := configutil.SetDevSpaceRoot()
	if err != nil {
		log.Fatal(err)
	}
	if !configExists {
		log.Fatal("Couldn't find a DevSpace configuration. Please run `devspace init`")
	}

	// Start file logging
	log.StartFileLogging()

	// Load config
	generatedConfig, err := generated.LoadConfig()
	if err != nil {
		log.Fatalf("Error loading generated.yaml: %v", err)
	}

	// Get the config
	config := cmd.loadConfig(generatedConfig)

	// Signal that we are working on the space if there is any
	err = cloud.ResumeSpace(config, generatedConfig, true, log.GetInstance())
	if err != nil {
		log.Fatal(err)
	}

	// Create kubectl client and switch context if specified
	client, err := kubectl.NewClientWithContextSwitch(config, cmd.SwitchContext)
	if err != nil {
		log.Fatalf("Unable to create new kubectl client: %v", err)
	}

	// Create namespace if necessary
	err = kubectl.EnsureDefaultNamespace(config, client, log.GetInstance())
	if err != nil {
		log.Fatalf("Unable to create namespace: %v", err)
	}

	// Create cluster role binding if necessary
	err = kubectl.EnsureGoogleCloudClusterRoleBinding(config, client, log.GetInstance())
	if err != nil {
		log.Fatalf("Unable to create ClusterRoleBinding: %v", err)
	}

	// Create the image pull secrets and add them to the default service account
	dockerClient, err := docker.NewClient(config, false, log.GetInstance())
	if err != nil {
		dockerClient = nil
	}

	err = registry.CreatePullSecrets(config, dockerClient, client, log.GetInstance())
	if err != nil {
		log.Fatal(err)
	}

	// Build and deploy images
	err = cmd.buildAndDeploy(config, generatedConfig, client, args)
	if err != nil {
		log.Fatal(err)
	}
}

func (cmd *DevCmd) buildAndDeploy(config *latest.Config, generatedConfig *generated.Config, client kubernetes.Interface, args []string) error {
	if cmd.SkipPipeline == false {
		// Dependencies
		err := dependency.DeployAll(config, generatedConfig, cmd.AllowCyclicDependencies, false, cmd.SkipPush, cmd.ForceDependencies, cmd.ForceBuild, cmd.ForceDeploy, log.GetInstance())
		if err != nil {
			log.Fatalf("Error deploying dependencies: %v", err)
		}

		// Build image if necessary
		builtImages, err := build.All(config, generatedConfig.GetActive(), client, cmd.SkipPush, true, cmd.ForceBuild, cmd.BuildSequential, log.GetInstance())
		if err != nil {
			if strings.Index(err.Error(), "no space left on device") != -1 {
				return fmt.Errorf("Error building image: %v\n\n Try running `%s` to free docker daemon space and retry", err, ansi.Color("devspace cleanup images", "white+b"))
			}

			return fmt.Errorf("Error building image: %v", err)
		}

		// Save config if an image was built
		if len(builtImages) > 0 {
			err := generated.SaveConfig(generatedConfig)
			if err != nil {
				return fmt.Errorf("Error saving generated config: %v", err)
			}
		}

		// Deploy all defined deployments
		if config.Deployments != nil {
			// What deployments should be deployed
			deployments := []string{}
			if cmd.Deployments != "" {
				deployments = strings.Split(cmd.Deployments, ",")
				for index := range deployments {
					deployments[index] = strings.TrimSpace(deployments[index])
				}
			}

			// Deploy all
			err = deploy.All(config, generatedConfig.GetActive(), client, true, cmd.ForceDeploy, builtImages, deployments, log.GetInstance())
			if err != nil {
				return fmt.Errorf("Error deploying: %v", err)
			}

			// Save Config
			err = generated.SaveConfig(generatedConfig)
			if err != nil {
				return fmt.Errorf("Error saving generated config: %v", err)
			}
		}
	}

	// Start services
	if cmd.ExitAfterDeploy == false {
		// Start services
		err := cmd.startServices(config, client, args, log.GetInstance())
		if err != nil {
			// Check if we should reload
			if _, ok := err.(*reloadError); ok {
				// Get the config
				config := cmd.loadConfig(generatedConfig)

				// Trigger rebuild & redeploy
				return cmd.buildAndDeploy(config, generatedConfig, client, args)
			}

			return err
		}
	}

	return nil
}

func (cmd *DevCmd) startServices(config *latest.Config, client kubernetes.Interface, args []string, log log.Logger) error {
	if cmd.Portforwarding {
		portForwarder, err := services.StartPortForwarding(config, client, log)
		if err != nil {
			return fmt.Errorf("Unable to start portforwarding: %v", err)
		}

		defer func() {
			for _, v := range portForwarder {
				v.Close()
			}
		}()
	}

	if cmd.Sync {
		syncConfigs, err := services.StartSync(config, cmd.VerboseSync, log)
		if err != nil {
			return fmt.Errorf("Unable to start sync: %v", err)
		}

		defer func() {
			for _, v := range syncConfigs {
				v.Stop(nil)
			}
		}()
	}

	exitChan := make(chan error)
	autoReloadPaths := GetPaths(config)

	// Start watcher if we have at least one auto reload path and if we should not skip the pipeline
	if cmd.SkipPipeline == false && len(autoReloadPaths) > 0 {
		var once sync.Once
		watcher, err := watch.New(autoReloadPaths, func(changed []string, deleted []string) error {
			once.Do(func() {
				log.Info("Change detected, will reload in 2 seconds")
				time.Sleep(time.Second * 2)

				exitChan <- &reloadError{}
			})

			return nil
		}, log)
		if err != nil {
			return err
		}

		watcher.Start()
		defer watcher.Stop()
	}

	// Build params
	params := targetselector.CmdParameter{}
	if cmd.Selector != "" {
		params.Selector = &cmd.Selector
	}
	if cmd.Container != "" {
		params.ContainerName = &cmd.Container
	}
	if cmd.LabelSelector != "" {
		params.LabelSelector = &cmd.LabelSelector
	}
	if cmd.Namespace != "" {
		params.Namespace = &cmd.Namespace
	}

	if cmd.Terminal && (config.Dev == nil || config.Dev.Terminal == nil || config.Dev.Terminal.Disabled == nil || *config.Dev.Terminal.Disabled == false) {
		return services.StartTerminal(config, client, params, args, exitChan, log)
	}

	log.Info("Will now try to print the logs of a running pod...")

	// Start attaching to a running pod
	err := services.StartAttach(config, client, params, exitChan, log)
	if err != nil {
		// If it's a reload error we return that so we can rebuild & redeploy
		if _, ok := err.(*reloadError); ok {
			return err
		}

		log.Infof("Couldn't print logs of running pod: %v", err)
	}

	log.Done("Services started (Press Ctrl+C to abort port-forwarding and sync)")
	return <-exitChan
}

// GetPaths retrieves the watch paths from the config object
func GetPaths(config *latest.Config) []string {
	paths := make([]string, 0, 1)

	// Add the deploy manifest paths
	if config.Dev != nil && config.Dev.AutoReload != nil {
		if config.Dev.AutoReload.Deployments != nil && config.Deployments != nil {
			for _, deployName := range *config.Dev.AutoReload.Deployments {
				for _, deployConf := range *config.Deployments {
					if *deployName == *deployConf.Name {
						if deployConf.Helm != nil && deployConf.Helm.Chart.Name != nil {
							_, err := os.Stat(*deployConf.Helm.Chart.Name)
							if err == nil {
								chartPath := *deployConf.Helm.Chart.Name
								if chartPath[len(chartPath)-1] != '/' {
									chartPath += "/"
								}

								paths = append(paths, chartPath+"**")
							}
						} else if deployConf.Kubectl != nil && deployConf.Kubectl.Manifests != nil {
							for _, manifestPath := range *deployConf.Kubectl.Manifests {
								paths = append(paths, *manifestPath)
							}
						}
					}
				}
			}
		}

		// Add the dockerfile paths
		if config.Dev.AutoReload.Images != nil && config.Images != nil {
			for _, imageName := range *config.Dev.AutoReload.Images {
				for imageConfName, imageConf := range *config.Images {
					if *imageName == imageConfName {
						dockerfilePath := "./Dockerfile"
						if imageConf.Dockerfile != nil {
							dockerfilePath = *imageConf.Dockerfile
						}

						paths = append(paths, dockerfilePath)
					}
				}
			}
		}

		// Add the additional paths
		if config.Dev.AutoReload.Paths != nil {
			for _, path := range *config.Dev.AutoReload.Paths {
				paths = append(paths, *path)
			}
		}
	}

	return paths
}

type reloadError struct {
}

func (r *reloadError) Error() string {
	return ""
}

func (cmd *DevCmd) loadConfig(generatedConfig *generated.Config) *latest.Config {
	// Load Config and modify it
	config, err := configutil.GetConfigFromPath(".", generatedConfig.ActiveConfig, true, generatedConfig, log.GetInstance())
	if err != nil {
		log.Fatal(err)
	}

	if cmd.Namespace != "" {
		config.Cluster = &v1.Cluster{
			Namespace:   &cmd.Namespace,
			KubeContext: config.Cluster.KubeContext,
		}

		log.Infof("Using %s namespace", cmd.Namespace)
	}

	// Save generated config
	err = generated.SaveConfig(generatedConfig)
	if err != nil {
		log.Fatalf("Couldn't save generated config: %v", err)
	}

	return config
}
