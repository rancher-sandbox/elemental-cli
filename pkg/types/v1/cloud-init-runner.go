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

package v1

import (
	"github.com/mudler/yip/pkg/console"
	"github.com/mudler/yip/pkg/executor"
	"github.com/mudler/yip/pkg/plugins"
	"github.com/mudler/yip/pkg/schema"
	"github.com/twpayne/go-vfs"
)

type CloudInitRunner interface {
	Run(string, ...string) error
}

type YipCloudInitRunner struct {
	exec    executor.Executor
	fs      vfs.FS
	console plugins.Console
}

// CloudInitRunner returns a default yip cloud init executor with the Elemental plugin set.
// It accepts a logger which is used inside the runner.
func NewYipCloudInitRunner(l Logger) YipCloudInitRunner {
	exec := executor.NewExecutor(
		executor.WithConditionals(
			plugins.NodeConditional,
			plugins.IfConditional,
		),
		executor.WithLogger(l),
		executor.WithPlugins(
			// Note, the plugin execution order depends on the order passed here
			plugins.DNS,
			plugins.Download,
			plugins.Git,
			plugins.Entities,
			plugins.EnsureDirectories,
			plugins.EnsureFiles,
			plugins.Commands,
			plugins.DeleteEntities,
			plugins.Hostname,
			plugins.Sysctl,
			plugins.SSH,
			plugins.User,
			plugins.LoadModules,
			plugins.Timesyncd,
			plugins.Systemctl,
			plugins.Environment,
			plugins.SystemdFirstboot,
			plugins.DataSources,
			plugins.Layout,
		),
	)
	return YipCloudInitRunner{
		exec: exec, fs: vfs.OSFS,
		console: console.NewStandardConsole(console.WithLogger(l)),
	}
}

func (ci YipCloudInitRunner) Run(stage string, args ...string) error {
	return ci.exec.Run(stage, ci.fs, ci.console, args...)
}

func (ci *YipCloudInitRunner) SetModifier(m schema.Modifier) {
	ci.exec.Modifier(m)
}

// Useful for testing purposes
func (ci *YipCloudInitRunner) SetFs(fs vfs.FS) {
	ci.fs = fs
}
