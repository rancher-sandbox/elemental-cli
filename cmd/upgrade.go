/*
Copyright © 2022 SUSE LLC

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
	"os/exec"

	"github.com/rancher-sandbox/elemental/cmd/config"
	"github.com/rancher-sandbox/elemental/pkg/action"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/mount-utils"
)

// NewUpgradeCmd returns a new instance of the upgrade subcommand and appends it to
// the root command. requireRoot is to initiate it with or without the CheckRoot
// pre-run check. This method is mostly used for testing purposes.
func NewUpgradeCmd(root *cobra.Command, addCheckRoot bool) *cobra.Command {
	c := &cobra.Command{
		Use:   "upgrade",
		Short: "upgrade the system",
		Args:  cobra.ExactArgs(0),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if addCheckRoot {
				return CheckRoot()
			}
			return nil
		},

		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := exec.LookPath("mount")
			if err != nil {
				return err
			}
			mounter := mount.New(path)

			cfg, err := config.ReadConfigRun(viper.GetString("config-dir"), cmd.Flags(), mounter)
			if err != nil {
				cfg.Logger.Errorf("Error reading config: %s\n", err)
			}

			if err := validateInstallUpgradeFlags(cfg.Logger, cmd.Flags()); err != nil {
				return err
			}

			// Adapt 'docker-image' and 'directory'  deprecated flags to 'system' syntax
			adaptDockerImageAndDirectoryFlagsToSystem()

			// TODO
			// Map environment variables to sub viper keys
			keyEnvMap := map[string]string{}

			// Set this after parsing of the flags, so it fails on parsing and prints usage properly
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true // Do not propagate errors down the line, we control them

			spec, err := config.ReadUpgradeSpec(cfg, cmd.Flags(), keyEnvMap)
			if err != nil {
				cfg.Logger.Errorf("invalid upgrade command setup %v", err)
				return err
			}

			cfg.Logger.Infof("Upgrade called")
			upgrade := action.NewUpgradeAction(cfg, spec)
			return upgrade.Run()
		},
	}
	root.AddCommand(c)
	c.Flags().Bool("recovery", false, "Upgrade the recovery")
	addSharedInstallUpgradeFlags(c)
	addSquashFsCompressionFlags(c)
	addLocalImageFlag(c)
	return c
}

// register the subcommand into rootCmd
var _ = NewUpgradeCmd(rootCmd, true)
