/*
Copyright © 2021 SUSE LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"fmt"
	"github.com/mudler/yip/pkg/schema"
	"github.com/rancher-sandbox/elemental-cli/cmd/config"
	"github.com/spf13/afero"
	"github.com/spf13/viper"
	"k8s.io/mount-utils"
	"os"
	"os/exec"
	"strings"

	v1 "github.com/rancher-sandbox/elemental-cli/pkg/types/v1"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// cloudInit represents the cloud-init command
var runStage = &cobra.Command{
	Use:   "run-stage STAGE",
	Short: "elemental run-stage",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var cmdLineYipUri string
		var FinalCloudInitPaths []string
		CloudInitPaths := []string{"/system/oem", "/oem/", "/usr/local/cloud-config/"}

		logger := logrus.New()
		logger.SetOutput(os.Stdout)

		cfg, err := config.ReadConfigRun(viper.GetString("config-dir"), logger, &mount.FakeMounter{})

		if err != nil {
			cfg.Logger.Errorf("Error reading config: %s\n", err)
		}

		// Check if we have extra cloud init
		if cfg.CloudInitPaths != "" {
			cfg.Logger.Debugf("Adding extra paths: %s", cfg.CloudInitPaths)
			extraCloudInitPathsSplit := strings.Split(cfg.CloudInitPaths, " ")
			CloudInitPaths = append(CloudInitPaths, extraCloudInitPathsSplit...)
		}

		// Cleanup paths. Check if they exist and add them to the final list to avoid failures on non-existing paths
		for _, path := range CloudInitPaths {
			exists, err := afero.Exists(afero.NewOsFs(), path)
			if exists && err == nil {
				FinalCloudInitPaths = append(FinalCloudInitPaths, path)
			} else {
				cfg.Logger.Debugf("Skipping path %s as it doesnt exists or cant access it", path)
			}
		}

		// Strip the stage value from args, we want to ignore everything else
		stage := args[0]
		stageBefore := fmt.Sprintf("%s.before", stage)
		stageAfter := fmt.Sprintf("%s.after", stage)

		// Check if the cmdline has the cos.setup key and extract its value to run yip on that given uri
		out, err := exec.Command("cat", "/proc/cmdline").CombinedOutput()
		if err != nil {
			return err
		}
		cmdLine := strings.Split(string(out), " ")
		for _, line := range cmdLine {
			if strings.Contains(line, "=") {
				lineSplit := strings.Split(line, "=")
				if lineSplit[0] == "cos.setup" {
					cmdLineYipUri = lineSplit[1]
					cfg.Logger.Debugf("Found cos.setup stanza on cmdline with value %s", cmdLineYipUri)
				}
			}
		}

		runner := v1.NewYipCloudInitRunner(logger)

		// Run the stage.before if cmdline contains the cos.setup stanza
		if cmdLineYipUri != "" {
			cmdLineArgs := []string{cmdLineYipUri}
			err := runner.Run(stageBefore, cmdLineArgs...)
			if err != nil {
				return err
			}
		}

		// Run all stages for each of the default cloud config paths + extra cloud config paths
		err = runner.Run(stageBefore, FinalCloudInitPaths...)
		if err != nil {
			return err
		}
		err = runner.Run(stage, FinalCloudInitPaths...)
		if err != nil {
			return err
		}
		err = runner.Run(stageAfter, FinalCloudInitPaths...)
		if err != nil {
			return err
		}

		// Run the stage.after if cmdline contains the cos.setup stanza
		if cmdLineYipUri != "" {
			cmdLineArgs := []string{cmdLineYipUri}
			err := runner.Run(stageAfter, cmdLineArgs...)
			if err != nil {
				return err
			}
		}

		// Finally, run all stages with dot notation using /proc/cmdline (why? how? is this used?)
		runner.SetModifier(schema.DotNotationModifier)
		err = runner.Run(stageBefore, "/proc/cmdline")
		if err != nil {
			return err
		}
		err = runner.Run(stage, "/proc/cmdline")
		if err != nil {
			return err
		}
		err = runner.Run(stageAfter, "/proc/cmdline")
		if err != nil {
			return err
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(runStage)
}
