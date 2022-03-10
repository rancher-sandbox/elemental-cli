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

package utils_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher-sandbox/elemental/pkg/action"
	conf "github.com/rancher-sandbox/elemental/pkg/config"
	"github.com/rancher-sandbox/elemental/pkg/constants"
	v1 "github.com/rancher-sandbox/elemental/pkg/types/v1"
	"github.com/rancher-sandbox/elemental/pkg/utils"
	v1mock "github.com/rancher-sandbox/elemental/tests/mocks"
	log "github.com/sirupsen/logrus"
	"github.com/twpayne/go-vfs"
	"github.com/twpayne/go-vfs/vfst"
)

func getNamesFromListFiles(list []os.FileInfo) []string {
	var names []string
	for _, f := range list {
		names = append(names, f.Name())
	}
	return names
}

var _ = Describe("Utils", Label("utils"), func() {
	var config *v1.RunConfig
	var runner *v1mock.FakeRunner
	var logger v1.Logger
	var syscall *v1mock.FakeSyscall
	var client *v1mock.FakeHTTPClient
	var mounter *v1mock.ErrorMounter
	var fs vfs.FS
	var cleanup func()

	BeforeEach(func() {
		runner = v1mock.NewFakeRunner()
		syscall = &v1mock.FakeSyscall{}
		mounter = v1mock.NewErrorMounter()
		client = &v1mock.FakeHTTPClient{}
		logger = v1.NewNullLogger()
		// Ensure /tmp exists in the VFS
		fs, cleanup, _ = vfst.NewTestFS(nil)
		fs.Mkdir("/tmp", os.ModePerm)
		fs.Mkdir("/run", os.ModePerm)
		fs.Mkdir("/etc", os.ModePerm)

		config = conf.NewRunConfig(
			v1.WithFs(fs),
			v1.WithRunner(runner),
			v1.WithLogger(logger),
			v1.WithMounter(mounter),
			v1.WithSyscall(syscall),
			v1.WithClient(client),
		)
	})
	AfterEach(func() { cleanup() })

	Describe("Chroot", Label("chroot"), func() {
		var chroot *utils.Chroot
		BeforeEach(func() {
			chroot = utils.NewChroot(
				"/whatever",
				config,
			)
		})
		Describe("on success", func() {
			It("command should be called in the chroot", func() {
				_, err := chroot.Run("chroot-command")
				Expect(err).To(BeNil())
				Expect(syscall.WasChrootCalledWith("/whatever")).To(BeTrue())
			})
			It("commands should be called with a customized chroot", func() {
				chroot.SetExtraMounts(map[string]string{"/real/path": "/in/chroot/path"})
				Expect(chroot.Prepare()).To(BeNil())
				defer chroot.Close()
				_, err := chroot.Run("chroot-command")
				Expect(err).To(BeNil())
				Expect(syscall.WasChrootCalledWith("/whatever")).To(BeTrue())
				_, err = chroot.Run("chroot-another-command")
				Expect(err).To(BeNil())
			})
			It("runs a callback in a custom chroot", func() {
				called := false
				callback := func() error {
					called = true
					return nil
				}
				err := chroot.RunCallback(callback)
				Expect(err).To(BeNil())
				Expect(syscall.WasChrootCalledWith("/whatever")).To(BeTrue())
				Expect(called).To(BeTrue())
			})
		})
		Describe("on failure", func() {
			It("should return error if chroot-command fails", func() {
				runner.ReturnError = errors.New("run error")
				_, err := chroot.Run("chroot-command")
				Expect(err).NotTo(BeNil())
				Expect(syscall.WasChrootCalledWith("/whatever")).To(BeTrue())
			})
			It("should return error if callback fails", func() {
				called := false
				callback := func() error {
					called = true
					return errors.New("Callback error")
				}
				err := chroot.RunCallback(callback)
				Expect(err).NotTo(BeNil())
				Expect(syscall.WasChrootCalledWith("/whatever")).To(BeTrue())
				Expect(called).To(BeTrue())
			})
			It("should return error if preparing twice before closing", func() {
				Expect(chroot.Prepare()).To(BeNil())
				defer chroot.Close()
				Expect(chroot.Prepare()).NotTo(BeNil())
				Expect(chroot.Close()).To(BeNil())
				Expect(chroot.Prepare()).To(BeNil())
			})
			It("should return error if failed to chroot", func() {
				syscall.ErrorOnChroot = true
				_, err := chroot.Run("chroot-command")
				Expect(err).ToNot(BeNil())
				Expect(syscall.WasChrootCalledWith("/whatever")).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("chroot error"))
			})
			It("should return error if failed to mount on prepare", Label("mount"), func() {
				mounter.ErrorOnMount = true
				_, err := chroot.Run("chroot-command")
				Expect(err).ToNot(BeNil())
				Expect(err.Error()).To(ContainSubstring("mount error"))
			})
			It("should return error if failed to unmount on close", Label("unmount"), func() {
				mounter.ErrorOnUnmount = true
				_, err := chroot.Run("chroot-command")
				Expect(err).ToNot(BeNil())
				Expect(err.Error()).To(ContainSubstring("failed closing chroot"))
			})
		})
	})
	Describe("TestBootedFrom", Label("BootedFrom"), func() {
		It("returns true if we are booting from label FAKELABEL", func() {
			runner.ReturnValue = []byte("")
			Expect(utils.BootedFrom(runner, "FAKELABEL")).To(BeFalse())
		})
		It("returns false if we are not booting from label FAKELABEL", func() {
			runner.ReturnValue = []byte("FAKELABEL")
			Expect(utils.BootedFrom(runner, "FAKELABEL")).To(BeTrue())
		})
	})
	Describe("GetDeviceByLabel", Label("GetDeviceByLabel"), func() {
		var cmds [][]string
		BeforeEach(func() {
			cmds = [][]string{
				{"udevadm", "settle"},
				{"blkid", "--label", "FAKE"},
			}
		})
		It("returns found device", func() {
			runner.ReturnValue = []byte("/some/device")
			out, err := utils.GetDeviceByLabel(runner, "FAKE", 1)
			Expect(err).To(BeNil())
			Expect(out).To(Equal("/some/device"))
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("fails to run blkid", func() {
			runner.ReturnError = errors.New("failed running blkid")
			_, err := utils.GetDeviceByLabel(runner, "FAKE", 1)
			Expect(err).NotTo(BeNil())
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("fails if no device is found in two attempts", func() {
			runner.ReturnValue = []byte("")
			_, err := utils.GetDeviceByLabel(runner, "FAKE", 2)
			Expect(err).NotTo(BeNil())
			Expect(runner.CmdsMatch(append(cmds, cmds...))).To(BeNil())
		})
	})
	Describe("CosignVerify", Label("cosign"), func() {
		It("runs a keyless verification", func() {
			_, err := utils.CosignVerify(fs, runner, "some/image:latest", "", true)
			Expect(err).To(BeNil())
			Expect(runner.CmdsMatch([][]string{{"cosign", "-d=true", "some/image:latest"}})).To(BeNil())
		})
		It("runs a verification using a public key", func() {
			_, err := utils.CosignVerify(fs, runner, "some/image:latest", "https://mykey.pub", false)
			Expect(err).To(BeNil())
			Expect(runner.CmdsMatch(
				[][]string{{"cosign", "-key", "https://mykey.pub", "some/image:latest"}},
			)).To(BeNil())
		})
		It("Fails to to create temporary directories", func() {
			_, err := utils.CosignVerify(vfs.NewReadOnlyFS(fs), runner, "some/image:latest", "", true)
			Expect(err).NotTo(BeNil())
		})
	})
	Describe("Reboot and shutdown", Label("reboot", "shutdown"), func() {
		It("reboots", func() {
			start := time.Now()
			utils.Reboot(runner, 2)
			duration := time.Since(start)
			Expect(runner.CmdsMatch([][]string{{"reboot", "-f"}})).To(BeNil())
			Expect(duration.Seconds() >= 2).To(BeTrue())
		})
		It("shuts down", func() {
			start := time.Now()
			utils.Shutdown(runner, 3)
			duration := time.Since(start)
			Expect(runner.CmdsMatch([][]string{{"poweroff", "-f"}})).To(BeNil())
			Expect(duration.Seconds() >= 3).To(BeTrue())
		})
	})
	Describe("GetFullDeviceByLabel", Label("GetFullDeviceByLabel", "partition", "types"), func() {
		var cmds [][]string
		BeforeEach(func() {
			cmds = [][]string{
				{"udevadm", "settle"},
				{"blkid", "--label", "FAKE"},
			}
		})
		It("returns found v1.Partition", func() {
			cmds = [][]string{
				{"udevadm", "settle"},
				{"blkid", "--label", "FAKE"},
				{"lsblk", "-p", "-b", "-n", "-J", "--output", "LABEL,SIZE,FSTYPE,MOUNTPOINT,PATH,PKNAME", "/dev/fake"},
			}
			runner.SideEffect = func(command string, args ...string) ([]byte, error) {
				if command == "blkid" && args[0] == "--label" && args[1] == "FAKE" {
					return []byte("/dev/fake"), nil
				}
				if command == "lsblk" {
					return []byte(`{"blockdevices":[{"label":"fake","size":1,"partlabel":"pfake","fstype":"fakefs","partflags":null,"mountpoint":"/mnt/fake"}]}`), nil
				}
				return nil, nil
			}
			out, err := utils.GetFullDeviceByLabel(runner, "FAKE", 1)
			var flags []string
			Expect(err).To(BeNil())
			Expect(out.Label).To(Equal("fake"))
			Expect(out.Size).To(Equal(uint(1)))
			Expect(out.FS).To(Equal("fakefs"))
			Expect(out.MountPoint).To(Equal("/mnt/fake"))
			Expect(out.Flags).To(Equal(flags))
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("fails to run blkid", func() {
			runner.ReturnError = errors.New("failed running blkid")
			_, err := utils.GetFullDeviceByLabel(runner, "FAKE", 1)
			Expect(err).To(HaveOccurred())
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("fails to run lsblk", func() {
			cmds = [][]string{
				{"udevadm", "settle"},
				{"blkid", "--label", "FAKE"},
				{"lsblk", "-p", "-b", "-n", "-J", "--output", "LABEL,SIZE,FSTYPE,MOUNTPOINT,PATH,PKNAME", "/dev/fake"},
			}
			runner.SideEffect = func(command string, args ...string) ([]byte, error) {
				if command == "blkid" && args[0] == "--label" && args[1] == "FAKE" {
					return []byte("/dev/fake"), nil
				}
				if command == "lsblk" {
					return nil, errors.New("error")
				}
				return nil, nil
			}
			_, err := utils.GetFullDeviceByLabel(runner, "FAKE", 1)
			Expect(err).To(HaveOccurred())
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("fails to parse json output", func() {
			cmds = [][]string{
				{"udevadm", "settle"},
				{"blkid", "--label", "FAKE"},
				{"lsblk", "-p", "-b", "-n", "-J", "--output", "LABEL,SIZE,FSTYPE,MOUNTPOINT,PATH,PKNAME", "/dev/fake"},
			}
			runner.SideEffect = func(command string, args ...string) ([]byte, error) {
				if command == "blkid" && args[0] == "--label" && args[1] == "FAKE" {
					return []byte("/dev/fake"), nil
				}
				if command == "lsblk" {
					return []byte("output changed"), nil
				}
				return nil, nil
			}
			_, err := utils.GetFullDeviceByLabel(runner, "FAKE", 1)
			Expect(err).To(HaveOccurred())
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("fails if no device is found in two attempts", func() {
			runner.ReturnValue = []byte("")
			_, err := utils.GetFullDeviceByLabel(runner, "FAKE", 2)
			Expect(err).To(HaveOccurred())
			Expect(runner.CmdsMatch(append(cmds, cmds...))).To(BeNil())
		})
	})
	Describe("CopyFile", Label("CopyFile"), func() {
		It("Copies source to target", func() {
			err := utils.MkdirAll(fs, "/some", os.ModePerm)
			Expect(err).ShouldNot(HaveOccurred())
			_, err = fs.Create("/some/file")
			Expect(err).ShouldNot(HaveOccurred())
			_, err = fs.Stat("/some/otherfile")
			Expect(err).Should(HaveOccurred())
			Expect(utils.CopyFile(fs, "/some/file", "/some/otherfile")).ShouldNot(HaveOccurred())
			_, err = fs.Stat("/some/otherfile")
			Expect(err).To(BeNil())
			e, err := utils.Exists(fs, "/some/otherfile")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e).To(BeTrue())
		})
		It("Fails to open non existing file", func() {
			err := utils.MkdirAll(fs, "/some", os.ModePerm)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(utils.CopyFile(fs, "/some/file", "/some/otherfile")).NotTo(BeNil())
			_, err = fs.Stat("/some/otherfile")
			Expect(err).NotTo(BeNil())
		})
		It("Fails to copy on non writable target", func() {
			err := utils.MkdirAll(fs, "/some", os.ModePerm)
			Expect(err).ShouldNot(HaveOccurred())
			fs.Create("/some/file")
			_, err = fs.Stat("/some/otherfile")
			Expect(err).NotTo(BeNil())
			fs = vfs.NewReadOnlyFS(fs)
			Expect(utils.CopyFile(fs, "/some/file", "/some/otherfile")).NotTo(BeNil())
			_, err = fs.Stat("/some/otherfile")
			Expect(err).NotTo(BeNil())
		})
	})
	Describe("CreateDirStructure", Label("CreateDirStructure"), func() {
		It("Creates essential directories", func() {
			Expect(utils.CreateDirStructure(fs, "/my/root")).To(BeNil())
			for _, dir := range []string{"sys", "proc", "dev", "tmp", "boot", "usr/local", "oem"} {
				_, err := fs.Stat(fmt.Sprintf("/my/root/%s", dir))
				Expect(err).To(BeNil())
			}
		})
		It("Fails on non writable target", func() {
			fs = vfs.NewReadOnlyFS(fs)
			Expect(utils.CreateDirStructure(fs, "/my/root")).NotTo(BeNil())
		})
	})
	Describe("SyncData", Label("SyncData"), func() {
		It("Copies all files from source to target", func() {
			sourceDir, err := os.MkdirTemp("", "elemental")
			Expect(err).To(BeNil())
			defer os.RemoveAll(sourceDir)
			destDir, err := os.MkdirTemp("", "elemental")
			Expect(err).To(BeNil())
			defer os.RemoveAll(destDir)

			for i := 0; i < 5; i++ {
				_, _ = os.CreateTemp(sourceDir, "file*")
			}

			Expect(utils.SyncData(nil, sourceDir, destDir)).To(BeNil())

			filesDest, err := ioutil.ReadDir(destDir)
			Expect(err).To(BeNil())

			destNames := getNamesFromListFiles(filesDest)
			filesSource, err := ioutil.ReadDir(sourceDir)
			Expect(err).To(BeNil())

			SourceNames := getNamesFromListFiles(filesSource)

			// Should be the same files in both dirs now
			Expect(destNames).To(Equal(SourceNames))
		})

		It("Copies all files from source to target respecting excludes", func() {
			sourceDir, err := os.MkdirTemp("", "elemental")
			Expect(err).To(BeNil())
			defer os.RemoveAll(sourceDir)
			destDir, err := os.MkdirTemp("", "elemental")
			Expect(err).To(BeNil())
			defer os.RemoveAll(destDir)

			os.MkdirAll(filepath.Join(sourceDir, "host"), os.ModePerm)
			os.MkdirAll(filepath.Join(sourceDir, "run"), os.ModePerm)
			for i := 0; i < 5; i++ {
				_, _ = os.CreateTemp(sourceDir, "file*")
			}

			Expect(utils.SyncData(nil, sourceDir, destDir, "host", "run")).To(BeNil())

			filesDest, err := ioutil.ReadDir(destDir)
			Expect(err).To(BeNil())

			destNames := getNamesFromListFiles(filesDest)

			filesSource, err := ioutil.ReadDir(sourceDir)
			Expect(err).To(BeNil())

			SourceNames := getNamesFromListFiles(filesSource)

			// Shouldn't be the same
			Expect(destNames).ToNot(Equal(SourceNames))
			expected := []string{}

			for _, s := range SourceNames {
				if s != "host" && s != "run" {
					expected = append(expected, s)
				}
			}
			Expect(destNames).To(Equal(expected))
		})

		It("should not fail if dirs are empty", func() {
			sourceDir, err := os.MkdirTemp("", "elemental")
			Expect(err).To(BeNil())
			defer os.RemoveAll(sourceDir)
			destDir, err := os.MkdirTemp("", "elemental")
			Expect(err).To(BeNil())
			defer os.RemoveAll(destDir)
			Expect(utils.SyncData(nil, sourceDir, destDir)).To(BeNil())
		})
		It("should fail if destination does not exist", func() {
			sourceDir, err := os.MkdirTemp("", "elemental")
			Expect(err).To(BeNil())
			defer os.RemoveAll(sourceDir)
			Expect(utils.SyncData(nil, sourceDir, "/welp")).NotTo(BeNil())
		})
		It("should fail if source does not exist", func() {
			destDir, err := os.MkdirTemp("", "elemental")
			Expect(err).To(BeNil())
			defer os.RemoveAll(destDir)
			Expect(utils.SyncData(nil, "/welp", destDir)).NotTo(BeNil())
		})
	})
	Describe("IsLocalUrl", Label("IsLocalUrl"), func() {
		It("Detects a local url", func() {
			local, err := utils.IsLocalURL("file://some/path")
			Expect(err).To(BeNil())
			Expect(local).To(BeTrue())
		})
		It("Detects a local path", func() {
			local, err := utils.IsLocalURL("/some/path")
			Expect(err).To(BeNil())
			Expect(local).To(BeTrue())
		})
		It("Detects a remote url", func() {
			local, err := utils.IsLocalURL("http://something.org")
			Expect(err).To(BeNil())
			Expect(local).To(BeFalse())
		})
		It("Fails on invalid URL", func() {
			local, err := utils.IsLocalURL("$htt:|//insane.stuff")
			Expect(err).NotTo(BeNil())
			Expect(local).To(BeFalse())
		})
	})
	Describe("GetSource", Label("GetSource"), func() {
		It("Fails on invalid url", func() {
			Expect(utils.GetSource(config, "$htt:|//insane.stuff", "/tmp/dest")).NotTo(BeNil())
		})
		It("Fails on readonly destination", func() {
			config.Fs = vfs.NewReadOnlyFS(fs)
			Expect(utils.GetSource(config, "http://something.org", "/tmp/dest")).NotTo(BeNil())
		})
		It("Fails on non existing local source", func() {
			Expect(utils.GetSource(config, "/some/missing/file", "/tmp/dest")).NotTo(BeNil())
		})
		It("Fails on http client error", func() {
			client.Error = true
			url := "https://missing.io"
			Expect(utils.GetSource(config, url, "/tmp/dest")).NotTo(BeNil())
			client.WasGetCalledWith(url)
		})
		It("Copies local file to destination", func() {
			fs.Create("/tmp/file")
			Expect(utils.GetSource(config, "file:///tmp/file", "/tmp/dest")).To(BeNil())
			_, err := fs.Stat("/tmp/dest")
			Expect(err).To(BeNil())
		})
	})
	Describe("Grub", Label("grub", "root"), func() {
		Describe("Install", func() {
			BeforeEach(func() {
				// Create iso dir so InstallImagesSetup does not fail to get a source
				_ = utils.MkdirAll(fs, constants.IsoBaseTree, os.ModeDir)
				config.Target = "/dev/test"
				action.SetPartitionsFromScratch(config)
				action.InstallImagesSetup(config)
			})
			It("installs with default values", func() {
				buf := &bytes.Buffer{}
				logger := log.New()
				logger.SetOutput(buf)

				err := utils.MkdirAll(fs, fmt.Sprintf("%s/grub2/", constants.StateDir), 0666)
				Expect(err).ShouldNot(HaveOccurred())

				err = utils.MkdirAll(fs, filepath.Dir(filepath.Join(config.Images.GetActive().MountPoint, constants.GrubConf)), os.ModePerm)
				Expect(err).ShouldNot(HaveOccurred())

				err = fs.WriteFile(filepath.Join(config.Images.GetActive().MountPoint, constants.GrubConf), []byte("console=tty1"), 0644)
				Expect(err).ShouldNot(HaveOccurred())

				config.Logger = logger
				config.GrubConf = "/etc/cos/grub.cfg"

				grub := utils.NewGrub(config)
				err = grub.Install()
				Expect(err).To(BeNil())

				Expect(buf).To(ContainSubstring("Installing GRUB.."))
				Expect(buf).To(ContainSubstring("Grub install to device /dev/test complete"))
				Expect(buf).ToNot(ContainSubstring("efi"))
				Expect(buf.String()).ToNot(ContainSubstring("Adding extra tty (serial) to grub.cfg"))
				targetGrub, err := fs.ReadFile(fmt.Sprintf("%s/grub2/grub.cfg", constants.StateDir))
				Expect(err).To(BeNil())
				// Should not be modified at all
				Expect(targetGrub).To(ContainSubstring("console=tty1"))

			})
			It("installs with efi on efi system", Label("efi"), func() {
				buf := &bytes.Buffer{}
				logger := log.New()
				logger.SetOutput(buf)
				logger.SetLevel(log.DebugLevel)

				err := utils.MkdirAll(fs, filepath.Dir(filepath.Join(config.Images.GetActive().MountPoint, constants.GrubConf)), os.ModePerm)
				Expect(err).ShouldNot(HaveOccurred())

				err = utils.MkdirAll(fs, filepath.Dir(constants.EfiDevice), os.ModePerm)
				Expect(err).ShouldNot(HaveOccurred())

				_, _ = fs.Create(filepath.Join(config.Images.GetActive().MountPoint, constants.GrubConf))
				_, _ = fs.Create(constants.EfiDevice)

				config.Logger = logger

				grub := utils.NewGrub(config)
				err = grub.Install()
				Expect(err).ShouldNot(HaveOccurred())

				Expect(buf.String()).To(ContainSubstring("--target=x86_64-efi"))
				Expect(buf.String()).To(ContainSubstring("--efi-directory"))
				Expect(buf.String()).To(ContainSubstring("Installing grub efi for arch x86_64"))
			})
			It("installs with efi with --force-efi", Label("efi"), func() {
				buf := &bytes.Buffer{}
				logger := log.New()
				logger.SetOutput(buf)
				logger.SetLevel(log.DebugLevel)

				err := utils.MkdirAll(fs, filepath.Dir(filepath.Join(config.Images.GetActive().MountPoint, constants.GrubConf)), os.ModePerm)
				Expect(err).ShouldNot(HaveOccurred())

				_, _ = fs.Create(filepath.Join(config.Images.GetActive().MountPoint, constants.GrubConf))

				config.Logger = logger
				config.ForceEfi = true

				grub := utils.NewGrub(config)
				err = grub.Install()
				Expect(err).To(BeNil())

				Expect(buf.String()).To(ContainSubstring("--target=x86_64-efi"))
				Expect(buf.String()).To(ContainSubstring("--efi-directory"))
				Expect(buf.String()).To(ContainSubstring("Installing grub efi for arch x86_64"))
			})
			It("installs with extra tty", func() {
				buf := &bytes.Buffer{}
				logger := log.New()
				logger.SetOutput(buf)

				fs.Mkdir("/dev", os.ModePerm)

				err := utils.MkdirAll(fs, fmt.Sprintf("%s/grub2/", constants.StateDir), 0666)
				Expect(err).ShouldNot(HaveOccurred())

				err = utils.MkdirAll(fs, filepath.Dir(filepath.Join(config.Images.GetActive().MountPoint, constants.GrubConf)), os.ModePerm)
				Expect(err).ShouldNot(HaveOccurred())

				err = fs.WriteFile(filepath.Join(config.Images.GetActive().MountPoint, constants.GrubConf), []byte("console=tty1"), 0644)
				Expect(err).ShouldNot(HaveOccurred())

				_, err = fs.Create("/dev/serial")
				Expect(err).ShouldNot(HaveOccurred())

				config.Logger = logger
				config.Tty = "serial"

				grub := utils.NewGrub(config)
				err = grub.Install()
				Expect(err).To(BeNil())

				Expect(buf.String()).To(ContainSubstring("Adding extra tty (serial) to grub.cfg"))
				targetGrub, err := fs.ReadFile(fmt.Sprintf("%s/grub2/grub.cfg", constants.StateDir))
				Expect(err).To(BeNil())
				Expect(targetGrub).To(ContainSubstring("console=tty1 console=serial"))
			})
			It("Fails if active image is unset", func() {
				config.Images.SetActive(nil)
				grub := utils.NewGrub(config)
				err := grub.Install()
				Expect(err).NotTo(BeNil())
			})
			It("Fails if it can't read grub config file", func() {
				buf := &bytes.Buffer{}
				logger := log.New()
				logger.SetOutput(buf)

				_ = utils.MkdirAll(fs, fmt.Sprintf("%s/grub2/", constants.StateDir), 0666)

				config.Logger = logger

				grub := utils.NewGrub(config)
				Expect(grub.Install()).NotTo(BeNil())

				Expect(buf).To(ContainSubstring("Failed reading grub config file"))
			})
		})
		Describe("SetPersistentVariables", func() {
			It("Sets the grub environment file", func() {
				grub := utils.NewGrub(config)
				Expect(grub.SetPersistentVariables(
					"somefile", map[string]string{"key1": "value1", "key2": "value2"},
				)).To(BeNil())
				Expect(runner.IncludesCmds([][]string{
					{"grub2-editenv", "somefile", "set", "key1=value1"},
					{"grub2-editenv", "somefile", "set", "key2=value2"},
				})).To(BeNil())
			})
			It("Fails running grub2-editenv", func() {
				runner.ReturnError = errors.New("grub error")
				grub := utils.NewGrub(config)
				Expect(grub.SetPersistentVariables(
					"somefile", map[string]string{"key1": "value1"},
				)).NotTo(BeNil())
				Expect(runner.CmdsMatch([][]string{
					{"grub2-editenv", "somefile", "set", "key1=value1"},
				})).To(BeNil())
			})
		})
	})

	Describe("CreateSquashFS", Label("CreateSquashFS"), func() {
		It("runs with no options if none given", func() {
			err := utils.CreateSquashFS(runner, logger, "source", "dest", []string{})
			Expect(runner.IncludesCmds([][]string{
				{"mksquashfs", "source", "dest"},
			})).To(BeNil())
			Expect(err).ToNot(HaveOccurred())
		})
		It("runs with options if given", func() {
			err := utils.CreateSquashFS(runner, logger, "source", "dest", constants.GetDefaultSquashfsOptions())
			cmd := []string{"mksquashfs", "source", "dest"}
			cmd = append(cmd, constants.GetDefaultSquashfsOptions()...)
			Expect(runner.IncludesCmds([][]string{
				cmd,
			})).To(BeNil())
			Expect(err).ToNot(HaveOccurred())
		})
		It("returns an error if it fails", func() {
			runner.ReturnError = errors.New("error")
			err := utils.CreateSquashFS(runner, logger, "source", "dest", []string{})
			Expect(runner.IncludesCmds([][]string{
				{"mksquashfs", "source", "dest"},
			})).To(BeNil())
			Expect(err).To(HaveOccurred())
		})
	})
	Describe("CommandExists", Label("CommandExists"), func() {
		It("returns false if command does not exists", func() {
			exists := utils.CommandExists("THISCOMMANDSHOULDNOTBETHERECOMEON")
			Expect(exists).To(BeFalse())
		})
		It("returns true if command exists", func() {
			exists := utils.CommandExists("true")
			Expect(exists).To(BeTrue())
		})
	})
	Describe("LoadEnvFile", Label("LoadEnvFile"), func() {
		BeforeEach(func() {
			fs.Mkdir("/etc", os.ModePerm)
		})
		It("returns proper map if file exists", func() {
			err := fs.WriteFile("/etc/envfile", []byte("TESTKEY=TESTVALUE"), os.ModePerm)
			Expect(err).ToNot(HaveOccurred())
			envData, err := utils.LoadEnvFile(fs, "/etc/envfile")
			Expect(err).ToNot(HaveOccurred())
			Expect(envData).To(HaveKeyWithValue("TESTKEY", "TESTVALUE"))
		})
		It("returns error if file doesnt exist", func() {
			_, err := utils.LoadEnvFile(fs, "/etc/envfile")
			Expect(err).To(HaveOccurred())
		})

		It("returns error if it cant unmarshall the env file", func() {
			err := fs.WriteFile("/etc/envfile", []byte("WHATWHAT"), os.ModePerm)
			Expect(err).ToNot(HaveOccurred())
			_, err = utils.LoadEnvFile(fs, "/etc/envfile")
			Expect(err).To(HaveOccurred())
		})
	})
	Describe("CleanStack", Label("CleanStack"), func() {
		var cleaner *utils.CleanStack
		BeforeEach(func() {
			cleaner = utils.NewCleanStack()
		})
		It("Adds a callback to the stack and pops it", func() {
			var flag bool
			callback := func() error {
				flag = true
				return nil
			}
			Expect(cleaner.Pop()).To(BeNil())
			cleaner.Push(callback)
			poppedJob := cleaner.Pop()
			Expect(poppedJob).NotTo(BeNil())
			poppedJob()
			Expect(flag).To(BeTrue())
		})
		It("On Cleanup runs callback stack in reverse order", func() {
			result := ""
			callback1 := func() error {
				result = result + "one "
				return nil
			}
			callback2 := func() error {
				result = result + "two "
				return nil
			}
			callback3 := func() error {
				result = result + "three "
				return nil
			}
			cleaner.Push(callback1)
			cleaner.Push(callback2)
			cleaner.Push(callback3)
			cleaner.Cleanup(nil)
			Expect(result).To(Equal("three two one "))
		})
		It("On Cleanup keeps former error and all callbacks are executed", func() {
			err := errors.New("Former error")
			count := 0
			callback := func() error {
				count++
				if count == 2 {
					return errors.New("Cleanup Error")
				}
				return nil
			}
			cleaner.Push(callback)
			cleaner.Push(callback)
			cleaner.Push(callback)
			err = cleaner.Cleanup(err)
			Expect(count).To(Equal(3))
			Expect(err.Error()).To(ContainSubstring("Former error"))
		})
		It("On Cleanup error reports first error and all callbacks are executed", func() {
			var err error
			count := 0
			callback := func() error {
				count++
				if count >= 2 {
					return errors.New(fmt.Sprintf("Cleanup error %d", count))
				}
				return nil
			}
			cleaner.Push(callback)
			cleaner.Push(callback)
			cleaner.Push(callback)
			err = cleaner.Cleanup(err)
			Expect(count).To(Equal(3))
			Expect(err.Error()).To(ContainSubstring("Cleanup error 2"))
			Expect(err.Error()).To(ContainSubstring("Cleanup error 3"))
		})
	})
})
