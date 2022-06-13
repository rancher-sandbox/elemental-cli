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

package config_test

import (
	"bytes"
	"os"
	"strings"

	"github.com/sanity-io/litter"

	. "github.com/rancher/elemental-cli/cmd/config"

	"github.com/jaypipes/ghw/pkg/block"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/twpayne/go-vfs"
	"github.com/twpayne/go-vfs/vfst"

	"github.com/rancher/elemental-cli/pkg/constants"
	v1 "github.com/rancher/elemental-cli/pkg/types/v1"
	v1mock "github.com/rancher/elemental-cli/tests/mocks"
)

var _ = Describe("Config", Label("config"), func() {
	var mounter *v1mock.ErrorMounter

	BeforeEach(func() {
		mounter = &v1mock.ErrorMounter{}
	})
	AfterEach(func() {
		viper.Reset()
	})

	Context("From fixtures", func() {
		Describe("read all specs", Label("install"), func() {
			It("reads values correctly", func() {
				cfg, err := ReadConfigRun("../../tests/fixtures/simple/", nil, mounter)
				Expect(err).ShouldNot(HaveOccurred())

				Expect(cfg.Config.Cosign).To(BeTrue(), litter.Sdump(cfg))

				up, err := ReadUpgradeSpec(cfg, nil)
				Expect(err).Should(HaveOccurred(), litter.Sdump(cfg))

				Expect(up.GrubDefEntry).To(Equal("so"))
				Expect(up.Active.Size).To(Equal(uint(2000)), litter.Sdump(up))

				inst, err := ReadInstallSpec(cfg, nil)
				Expect(err).Should(HaveOccurred(), litter.Sdump(cfg))

				Expect(inst.GrubDefEntry).To(Equal("mockme"))
				Expect(inst.Active.Size).To(Equal(uint(2000)), litter.Sdump(up))
			})
		})
	})

	Describe("Build config", Label("build"), func() {
		var flags *pflag.FlagSet
		BeforeEach(func() {
			flags = pflag.NewFlagSet("testflags", 1)
			flags.String("arch", "", "testing flag")
			flags.Set("arch", "arm64")
		})
		It("values empty if config path not valid", Label("path", "values"), func() {
			cfg, err := ReadConfigBuild("/none/", flags, mounter)
			Expect(err).To(BeNil())
			Expect(viper.GetString("name")).To(Equal(""))
			Expect(cfg.Name).To(Equal("elemental"))
			Expect(cfg.Arch).To(Equal("arm64"))
		})
		It("values filled if config path valid", Label("path", "values"), func() {
			cfg, err := ReadConfigBuild("../../tests/fixtures/config/", flags, mounter)
			Expect(err).To(BeNil())
			Expect(viper.GetString("name")).To(Equal("cOS-0"))
			Expect(cfg.Name).To(Equal("cOS-0"))
			hasSuffix := strings.HasSuffix(viper.ConfigFileUsed(), "config/manifest.yaml")
			Expect(hasSuffix).To(BeTrue())
		})

		It("overrides values with env values", Label("env", "values"), func() {
			_ = os.Setenv("ELEMENTAL_BUILD_NAME", "randomname")
			cfg, err := ReadConfigBuild("../../tests/fixtures/config/", flags, mounter)
			Expect(err).To(BeNil())
			Expect(cfg.Name).To(Equal("randomname"))
		})
	})
	Describe("Read build specs", Label("build"), func() {
		var cfg *v1.BuildConfig
		var runner *v1mock.FakeRunner
		var fs vfs.FS
		var logger v1.Logger
		var mounter *v1mock.ErrorMounter
		var syscall *v1mock.FakeSyscall
		var client *v1mock.FakeHTTPClient
		var cloudInit *v1mock.FakeCloudInitRunner
		var cleanup func()
		var memLog *bytes.Buffer
		var err error

		BeforeEach(func() {
			runner = v1mock.NewFakeRunner()
			syscall = &v1mock.FakeSyscall{}
			mounter = v1mock.NewErrorMounter()
			client = &v1mock.FakeHTTPClient{}
			memLog = &bytes.Buffer{}
			logger = v1.NewBufferLogger(memLog)
			cloudInit = &v1mock.FakeCloudInitRunner{}

			fs, cleanup, err = vfst.NewTestFS(map[string]interface{}{})
			Expect(err).Should(BeNil())

			cfg, err = ReadConfigBuild("../../tests/fixtures/config/", nil, mounter)
			Expect(err).Should(BeNil())
			// From defaults
			Expect(cfg.Arch).To(Equal("x86_64"))

			// From config
			Expect(cfg.Repos[0].URI).To(ContainSubstring("registry.org/my/repo"))

			cfg.Fs = fs
			cfg.Runner = runner
			cfg.Logger = logger
			cfg.Mounter = mounter
			cfg.Syscall = syscall
			cfg.Client = client
			cfg.CloudInitRunner = cloudInit
		})
		AfterEach(func() {
			cleanup()
		})

		Describe("LiveISO spec", Label("iso"), func() {
			It("initiates a LiveISO spec", func() {
				iso, err := ReadBuildISO(cfg, nil)
				Expect(err).ShouldNot(HaveOccurred())

				// By default
				Expect(iso.HybridMBR).To(Equal(constants.IsoHybridMBR))

				// From config file
				Expect(iso.Image[0].Value()).To(Equal("recovery/cos-img"))
				Expect(iso.Label).To(Equal("LIVE_LABEL"))
			})
		})
		Describe("RawDisk spec", Label("disk"), func() {
			It("initiates a RawDisk spec", func() {
				disk, err := ReadBuildDisk(cfg, nil)
				Expect(err).ShouldNot(HaveOccurred())

				// From config file
				Expect(len(disk.X86_64.Packages)).To(Equal(1))
				Expect(disk.X86_64.Packages[0].Name).To(Equal("system/myos"))
			})
		})
	})
	Describe("Run config", Label("run"), func() {
		var flags *pflag.FlagSet
		BeforeEach(func() {
			flags = pflag.NewFlagSet("testflags", 1)
			flags.Bool("cosign", false, "testing flag")
			flags.String("cosign-key", "", "testing flag")
			flags.Set("cosign", "true")
			flags.Set("cosign-key", "someOtherKey")
		})
		It("uses defaults if no configs are provided", func() {
			cfg, err := ReadConfigRun("", nil, mounter)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(cfg.Arch == "x86_64").To(BeTrue())
			// Uses given mounter
			Expect(cfg.Mounter == mounter).To(BeTrue())
			// Sets a RealRunner instance by default
			Expect(cfg.Runner != nil).To(BeTrue())
			_, ok := cfg.Runner.(*v1.RealRunner)
			Expect(ok).To(BeTrue())
		})
		It("uses provided configs and flags, flags have priority", func() {
			cfg, err := ReadConfigRun("../../tests/fixtures/config/", flags, mounter)
			Expect(err).To(BeNil())
			Expect(cfg.Cosign).To(BeTrue())
			// Flags overwrite the cosign-key set in config
			Expect(cfg.CosignPubKey == "someOtherKey").To(BeTrue())
			// Config.d overwrites the main config.yaml
			Expect(len(cfg.CloudInitPaths) == 1).To(BeTrue())
			Expect(cfg.CloudInitPaths[0] == "some/other/path").To(BeTrue())
			Expect(len(cfg.Repos)).To(Equal(1))
			Expect(cfg.Repos[0].Name == "testrepo").To(BeTrue())
		})
		It("sets log level debug based on debug flag", func() {
			// Default value
			cfg, err := ReadConfigRun("../../tests/fixtures/config/", nil, mounter)
			Expect(err).To(BeNil())
			debug := viper.GetBool("debug")
			Expect(cfg.Logger.GetLevel()).ToNot(Equal(logrus.DebugLevel))
			Expect(debug).To(BeFalse())

			// Set it via viper, like the flag
			viper.Set("debug", true)
			cfg, err = ReadConfigRun("../../tests/fixtures/config/", nil, mounter)
			Expect(err).To(BeNil())
			debug = viper.GetBool("debug")
			Expect(debug).To(BeTrue())
			Expect(cfg.Logger.GetLevel()).To(Equal(logrus.DebugLevel))
		})
	})
	Describe("Read runtime specs", Label("spec"), func() {
		var cfg *v1.RunConfig
		var runner *v1mock.FakeRunner
		var fs vfs.FS
		var logger v1.Logger
		var mounter *v1mock.ErrorMounter
		var syscall *v1mock.FakeSyscall
		var client *v1mock.FakeHTTPClient
		var cloudInit *v1mock.FakeCloudInitRunner
		var cleanup func()
		var memLog *bytes.Buffer
		var err error

		BeforeEach(func() {
			runner = v1mock.NewFakeRunner()
			syscall = &v1mock.FakeSyscall{}
			mounter = v1mock.NewErrorMounter()
			client = &v1mock.FakeHTTPClient{}
			memLog = &bytes.Buffer{}
			logger = v1.NewBufferLogger(memLog)
			cloudInit = &v1mock.FakeCloudInitRunner{}

			fs, cleanup, err = vfst.NewTestFS(map[string]interface{}{})
			Expect(err).Should(BeNil())

			cfg, err = ReadConfigRun("../../tests/fixtures/config/", nil, mounter)
			Expect(err).Should(BeNil())

			cfg.Fs = fs
			cfg.Runner = runner
			cfg.Logger = logger
			cfg.Mounter = mounter
			cfg.Syscall = syscall
			cfg.Client = client
			cfg.CloudInitRunner = cloudInit
		})
		AfterEach(func() {
			cleanup()
		})
		Describe("Read InstallSpec", Label("install"), func() {
			var flags *pflag.FlagSet

			BeforeEach(func() {
				flags = pflag.NewFlagSet("testflags", 1)
				flags.String("system.uri", "", "testing flag")
				flags.Set("system.uri", "docker:image/from:flag")
			})
			It("inits a default install spec if no configs are provided", func() {
				spec, err := ReadInstallSpec(cfg, nil)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(spec.Target == "")
				Expect(spec.PartTable == v1.GPT)
				Expect(spec.Firmware == v1.BIOS)
				Expect(spec.NoFormat == false)
			})
			It("inits an install spec according to given configs", func() {
				err := os.Setenv("ELEMENTAL_INSTALL_TARGET", "/env/disk")
				Expect(err).ShouldNot(HaveOccurred())
				err = os.Setenv("ELEMENTAL_INSTALL_SYSTEM", "itwillbeignored")
				Expect(err).ShouldNot(HaveOccurred())

				spec, err := ReadInstallSpec(cfg, flags)
				Expect(err).ShouldNot(HaveOccurred())
				// Overwrites target from environment variables
				Expect(spec.Target == "/env/disk")
				// Overwrites system image, flags have priority over files and env vars
				Expect(spec.Active.Source.Value() == "image/from:flag")
				// Uses recovery and no-format defined in confing.yaml
				Expect(spec.Recovery.Source.Value() == "recovery/image:latest")
				Expect(spec.NoFormat == true)
			})
		})
		Describe("Read ResetSpec", Label("install"), func() {
			var flags *pflag.FlagSet
			var bootedFrom string
			var ghwTest v1mock.GhwMock

			BeforeEach(func() {
				bootedFrom = constants.SystemLabel
				flags = pflag.NewFlagSet("testflags", 1)
				flags.String("system.uri", "", "testing flag")
				flags.Set("system.uri", "docker:image/from:flag")

				runner.SideEffect = func(cmd string, args ...string) ([]byte, error) {
					switch cmd {
					case "cat":
						return []byte(bootedFrom), nil
					default:
						return []byte{}, nil
					}
				}
				mainDisk := block.Disk{
					Name: "device",
					Partitions: []*block.Partition{
						{
							Name:       "device2",
							Label:      "COS_STATE",
							Type:       "ext4",
							MountPoint: constants.RunningStateDir,
						},
					},
				}
				ghwTest = v1mock.GhwMock{}
				ghwTest.AddDisk(mainDisk)
				ghwTest.CreateDevices()
			})
			AfterEach(func() {
				ghwTest.Clean()
			})
			It("can't init reset spec if not booted from recovery", func() {
				// Disable recovery boot detection
				bootedFrom = ""

				_, err := ReadResetSpec(cfg, nil)
				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("reset can only be called from the recovery system"))
			})
			It("inits a reset spec according to given configs", func() {
				err := os.Setenv("ELEMENTAL_RESET_TARGET", "/special/disk")
				Expect(err).ShouldNot(HaveOccurred())
				err = os.Setenv("ELEMENTAL_RESET_SYSTEM", "channel:system/cos")
				Expect(err).ShouldNot(HaveOccurred())
				spec, err := ReadResetSpec(cfg, nil)
				Expect(err).ShouldNot(HaveOccurred())
				// Overwrites target from environment variables
				Expect(spec.Target == "/special/disk")
				// Overwrites system image, flags have priority over files and env vars
				Expect(spec.Active.Source.Value() == "image/from:flag")
				// From config files
				Expect(spec.Tty == "ttyS1")
			})
		})
		Describe("Read UpgradeSpec", Label("install"), func() {
			var flags *pflag.FlagSet
			var ghwTest v1mock.GhwMock

			BeforeEach(func() {
				flags = pflag.NewFlagSet("testflags", 1)
				flags.String("recovery-system.uri", "", "testing flag")
				flags.Set("recovery-system.uri", "docker:image/from:flag")
			})
			It("can't init upgrade spec if partitions are not found", func() {
				_, err := ReadUpgradeSpec(cfg, nil)
				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("undefined state partition"))
			})
			It("inits an upgrade spec according to given configs", func() {
				mainDisk := block.Disk{
					Name: "device",
					Partitions: []*block.Partition{
						{
							Name:       "device2",
							Label:      "COS_STATE",
							Type:       "ext4",
							MountPoint: constants.RunningStateDir,
						},
						{
							Name:       "device3",
							Label:      "COS_RECOVERY",
							Type:       "ext4",
							MountPoint: constants.RunningStateDir,
						},
					},
				}
				ghwTest = v1mock.GhwMock{}
				ghwTest.AddDisk(mainDisk)
				ghwTest.CreateDevices()
				defer ghwTest.Clean()

				err := os.Setenv("ELEMENTAL_UPGRADE_RECOVERY", "true")
				spec, err := ReadUpgradeSpec(cfg, nil)
				Expect(err).ShouldNot(HaveOccurred())
				// Overwrites recovery-system image, flags have priority over files and env vars
				Expect(spec.Recovery.Source.Value() == "image/from:flag")
				// System image from config files
				Expect(spec.Active.Source.Value() == "system/cos")
				// Sets recovery upgrade from environment variables
				Expect(spec.RecoveryUpgrade).To(BeTrue())
			})
		})

	})
})
