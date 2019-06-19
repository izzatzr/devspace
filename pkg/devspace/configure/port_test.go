package configure

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/devspace-cloud/devspace/pkg/devspace/config/configutil"
	"github.com/devspace-cloud/devspace/pkg/devspace/config/versions/latest"
	"github.com/devspace-cloud/devspace/pkg/util/ptr"

	"gotest.tools/assert"
)

func TestGetNameOfFirstHelmDeployment(t *testing.T) {
	assert.Equal(t, "foundIt", GetNameOfFirstHelmDeployment(&latest.Config{
		Deployments: &[]*latest.DeploymentConfig{
			&latest.DeploymentConfig{
				Helm: &latest.HelmConfig{},
				Name: ptr.String("foundIt"),
			},
		},
	}), "Got wrong name of first helmDeployment")

	assert.Equal(t, "devspace", GetNameOfFirstHelmDeployment(&latest.Config{}), "Got wrong default name")
}

func TestAddAndRemovePort(t *testing.T) {
	//Create tempDir and go into it
	dir, err := ioutil.TempDir("", "testDir")
	if err != nil {
		t.Fatalf("Error creating temporary directory: %v", err)
	}

	wdBackup, err := os.Getwd()
	if err != nil {
		t.Fatalf("Error getting current working directory: %v", err)
	}
	err = os.Chdir(dir)
	if err != nil {
		t.Fatalf("Error changing working directory: %v", err)
	}

	// Delete temp folder after test
	defer func() {
		err = os.Chdir(wdBackup)
		if err != nil {
			t.Fatalf("Error changing dir back: %v", err)
		}
		err = os.RemoveAll(dir)
		if err != nil {
			t.Fatalf("Error removing dir: %v", err)
		}
	}()

	config := configutil.GetBaseConfig()
	//err = AddPort("myns", "", "myservice", []string{"8080:8080"})
	//assert.NilError(t, err, "Error adding basic port")
	//assert.Equal(t, 1, len(*config.Dev.Ports), "Port not added")

	err = AddPort("myns", "a=b", "myservice", []string{"8080:8080"})
	assert.Error(t, err, "both service and label-selector specified. This is illegal because the label-selector is already specified in the referenced service. Therefore defining both is redundant")

	config.Dev.Selectors = &[]*latest.SelectorConfig{
		&latest.SelectorConfig{
			Name: ptr.String("FirstSelector"),
			LabelSelector: &map[string]*string{
				"index": ptr.String("first"),
			},
		},
		&latest.SelectorConfig{
			Name: ptr.String("SecoundSelector"),
			LabelSelector: &map[string]*string{
				"index": ptr.String("secound"),
			},
		},
	}
	err = AddPort("myns", "", "SecoundSelector", []string{"8081:8081"})
	assert.Equal(t, 1, len(*config.Dev.Ports), "Port not added")
	assert.Equal(t, 1, len(*(*config.Dev.Ports)[0].LabelSelector), "Wrong port added")

	err = AddPort("myns", "a=b", "", []string{"8081:8081"})
	assert.Equal(t, 3, len(*config.Dev.Ports), "Port not added")
	assert.Equal(t, 1, len(*(*config.Dev.Ports)[2].LabelSelector), "Wrong port added")
	assert.Equal(t, "b", *(*(*config.Dev.Ports)[2].LabelSelector)["a"], "Wrong port added")

}
