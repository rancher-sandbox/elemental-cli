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

package utils

import (
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/go-multierror"
	"github.com/mudler/yip/pkg/schema"
	"github.com/rancher-sandbox/elemental/pkg/constants"
	v1 "github.com/rancher-sandbox/elemental/pkg/types/v1"
)

// RunStage will run yip
func RunStage(stage string, cfg *v1.RunConfig) error {
	var cmdLineYipURI string
	var errors error
	CloudInitPaths := constants.GetCloudInitPaths()

	// Check if we have extra cloud init
	// This requires fixing the env vars, otherwise it wont work
	if cfg.CloudInitPaths != "" {
		cfg.Logger.Debugf("Adding extra paths: %s", cfg.CloudInitPaths)
		extraCloudInitPathsSplit := strings.Split(cfg.CloudInitPaths, " ")
		CloudInitPaths = append(CloudInitPaths, extraCloudInitPathsSplit...)
	}

	stageBefore := fmt.Sprintf("%s.before", stage)
	stageAfter := fmt.Sprintf("%s.after", stage)

	// Check if the cmdline has the cos.setup key and extract its value to run yip on that given uri
	cmdLineOut, err := cfg.Runner.Run("cat", "/proc/cmdline")
	if err != nil {
		errors = multierror.Append(errors, err)
	}
	cmdLine := strings.Split(string(cmdLineOut), " ")
	for _, line := range cmdLine {
		if strings.Contains(line, "=") {
			lineSplit := strings.Split(line, "=")
			if lineSplit[0] == "cos.setup" {
				cmdLineYipURI = lineSplit[1]
				cfg.Logger.Debugf("Found cos.setup stanza on cmdline with value %s", cmdLineYipURI)
			}
		}
	}

	// Run the stage.before if cmdline contains the cos.setup stanza
	if cmdLineYipURI != "" {
		cmdLineArgs := []string{cmdLineYipURI}
		err = cfg.CloudInitRunner.Run(stageBefore, cmdLineArgs...)
		if err != nil {
			errors = multierror.Append(errors, err)
		}
	}

	// Blindly try to create the dir that we are gonna pass to yip to avoid yip trying to unmarshal a non.existing dir
	for _, path := range CloudInitPaths {
		// We dont care if it fails to create, thats a different issue altogether
		_ = cfg.Fs.MkdirAll(path, os.ModeDir)
	}

	// Run all stages for each of the default cloud config paths + extra cloud config paths
	err = cfg.CloudInitRunner.Run(stageBefore, CloudInitPaths...)
	if err != nil {
		errors = multierror.Append(errors, err)
	}
	err = cfg.CloudInitRunner.Run(stage, CloudInitPaths...)
	if err != nil {
		errors = multierror.Append(errors, err)
	}
	err = cfg.CloudInitRunner.Run(stageAfter, CloudInitPaths...)
	if err != nil {
		errors = multierror.Append(errors, err)
	}

	// Run the stage.after if cmdline contains the cos.setup stanza
	if cmdLineYipURI != "" {
		cmdLineArgs := []string{cmdLineYipURI}
		err = cfg.CloudInitRunner.Run(stageAfter, cmdLineArgs...)
		if err != nil {
			errors = multierror.Append(errors, err)
		}
	}

	cfg.CloudInitRunner.SetModifier(schema.DotNotationModifier)
	// After RunStage reset the modifier
	defer cfg.CloudInitRunner.SetModifier(nil)

	err = cfg.CloudInitRunner.Run(stageBefore, string(cmdLineOut))
	if err != nil {
		errors = multierror.Append(errors, err)
	}
	err = cfg.CloudInitRunner.Run(stage, string(cmdLineOut))
	if err != nil {
		errors = multierror.Append(errors, err)
	}
	err = cfg.CloudInitRunner.Run(stageAfter, string(cmdLineOut))
	if err != nil {
		errors = multierror.Append(errors, err)
	}

	if errors != nil && !cfg.Strict {
		cfg.Logger.Info("Some errors found but were ignored. Enable --strict mode to fail on those or --debug to see them in the log")
		cfg.Logger.Warn(errors)
		return nil
	}

	return errors
}
