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
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
)

var _ = Describe("Install", Label("install", "cmd"), func() {
	var buf *bytes.Buffer
	BeforeEach(func() {
		rootCmd = NewRootCmd()
		_ = NewInstallCmd(rootCmd, false)
		buf = new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
	})
	AfterEach(func() {
		viper.Reset()
	})
	It("Errors out setting consign-key without setting cosign", Label("flags"), func() {
		_, _, err := executeCommandC(rootCmd, "install", "--cosign-key", "pubKey.url", "/dev/whatever")
		Expect(err).ToNot(BeNil())
		Expect(buf.String()).To(ContainSubstring("Usage:"))
		Expect(err.Error()).To(ContainSubstring("'cosign-key' requires 'cosign' option to be enabled"))
	})
	It("Errors out setting directory and docker-image at the same time", Label("flags"), func() {
		_, _, err := executeCommandC(rootCmd, "install", "--directory", "dir", "--docker-image", "image", "/dev/whatever")
		Expect(err).ToNot(BeNil())
		Expect(buf.String()).To(ContainSubstring("Usage:"))
		Expect(err.Error()).To(ContainSubstring("flags docker-image, directory and system are mutually exclusive"))
	})
	It("outputs usage if no DEVICE param", Label("args"), func() {
		_, _, err := executeCommandC(rootCmd, "install")
		Expect(err).ToNot(BeNil())
		Expect(buf.String()).To(ContainSubstring("at least a target device must be supplied"))
	})
	It("Errors out setting reboot and poweroff at the same time", Label("flags"), func() {
		_, _, err := executeCommandC(rootCmd, "install", "--reboot", "--poweroff", "/dev/whatever")
		Expect(err).ToNot(BeNil())
		Expect(buf.String()).To(ContainSubstring("Usage:"))
		Expect(err.Error()).To(ContainSubstring("'reboot' and 'poweroff' are mutually exclusive options"))
	})
})
