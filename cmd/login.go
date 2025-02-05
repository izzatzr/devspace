package cmd

import (
	"github.com/devspace-cloud/devspace/pkg/devspace/cloud"
	cloudconfig "github.com/devspace-cloud/devspace/pkg/devspace/cloud/config"
	"github.com/devspace-cloud/devspace/pkg/util/log"
	"github.com/spf13/cobra"
)

// LoginCmd holds the login cmd flags
type LoginCmd struct {
	Key      string
	Provider string
}

// NewLoginCmd creates a new login command
func NewLoginCmd() *cobra.Command {
	cmd := &LoginCmd{}

	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Log into DevSpace Cloud",
		Long: `
#######################################################
################### devspace login ####################
#######################################################
If no --key is supplied the browser will be opened 
and the login page is shown

Example:
devspace login
devspace login --key myaccesskey
#######################################################
	`,
		Args: cobra.NoArgs,
		Run:  cmd.RunLogin,
	}

	loginCmd.Flags().StringVar(&cmd.Key, "key", "", "Access key to use")
	loginCmd.Flags().StringVar(&cmd.Provider, "provider", "", "Provider to use")

	return loginCmd
}

// RunLogin executes the functionality devspace login
func (cmd *LoginCmd) RunLogin(cobraCmd *cobra.Command, args []string) {
	providerConfig, err := cloudconfig.ParseProviderConfig()
	if err != nil {
		log.Fatal(err)
	}

	providerName := cloudconfig.DevSpaceCloudProviderName
	if providerConfig.Default != "" {
		providerName = providerConfig.Default
	}
	if cmd.Provider != "" {
		providerName = cmd.Provider
	}

	if cmd.Key != "" {
		err = cloud.ReLogin(providerConfig, providerName, &cmd.Key, log.GetInstance())
		if err != nil {
			log.Fatalf("Error logging in: %v", err)
		}
	} else {
		err = cloud.ReLogin(providerConfig, providerName, nil, log.GetInstance())
		if err != nil {
			log.Fatalf("Error logging in: %v", err)
		}
	}

	log.Infof("Successful logged into %s", providerName)
}
