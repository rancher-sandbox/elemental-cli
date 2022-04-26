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

package action_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	dockerArchive "github.com/docker/docker/pkg/archive"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher-sandbox/elemental/pkg/action"
	"github.com/rancher-sandbox/elemental/pkg/config"
	"github.com/rancher-sandbox/elemental/pkg/constants"
	v1 "github.com/rancher-sandbox/elemental/pkg/types/v1"
	"github.com/rancher-sandbox/elemental/pkg/utils"
	v1mock "github.com/rancher-sandbox/elemental/tests/mocks"
	"github.com/sirupsen/logrus"
	"github.com/twpayne/go-vfs"
	"github.com/twpayne/go-vfs/vfst"
)

var _ = Describe("Runtime Actions", func() {
	var cfg *v1.BuildConfig
	var runner *v1mock.FakeRunner
	var fs vfs.FS
	var logger v1.Logger
	var mounter *v1mock.ErrorMounter
	var syscall *v1mock.FakeSyscall
	var client *v1mock.FakeHTTPClient
	var cloudInit *v1mock.FakeCloudInitRunner
	var luet *v1mock.FakeLuet
	var cleanup func()
	var memLog *bytes.Buffer
	BeforeEach(func() {
		runner = v1mock.NewFakeRunner()
		syscall = &v1mock.FakeSyscall{}
		mounter = v1mock.NewErrorMounter()
		client = &v1mock.FakeHTTPClient{}
		memLog = &bytes.Buffer{}
		logger = v1.NewBufferLogger(memLog)
		logger.SetLevel(logrus.DebugLevel)
		cloudInit = &v1mock.FakeCloudInitRunner{}
		luet = &v1mock.FakeLuet{}
		fs, cleanup, _ = vfst.NewTestFS(map[string]interface{}{})
		cfg = config.NewBuildConfig(
			config.WithFs(fs),
			config.WithRunner(runner),
			config.WithLogger(logger),
			config.WithMounter(mounter),
			config.WithSyscall(syscall),
			config.WithClient(client),
			config.WithCloudInitRunner(cloudInit),
			config.WithLuet(luet),
		)
	})
	AfterEach(func() {
		cleanup()
	})
	Describe("Build ISO", Label("iso"), func() {
		It("Successfully builds an ISO from a Docker image", func() {
			cfg.Date = true
			cfg.ISO.RootFS = []string{"elementalos:latest"}
			cfg.ISO.UEFI = []string{"live/efi"}
			cfg.ISO.Image = []string{"live/bootloader"}

			luet.UnpackSideEffect = func(target string, image string, local bool) error {
				bootDir := filepath.Join(target, "boot")
				err := utils.MkdirAll(fs, bootDir, constants.DirPerm)
				if err != nil {
					return err
				}
				_, err = fs.Create(filepath.Join(bootDir, "vmlinuz"))
				if err != nil {
					return err
				}
				_, err = fs.Create(filepath.Join(bootDir, "initrd"))
				if err != nil {
					return err
				}
				return nil
			}

			err := action.BuildISORun(cfg)

			Expect(luet.UnpackCalled()).To(BeTrue())
			Expect(luet.UnpackChannelCalled()).To(BeTrue())
			Expect(err).ShouldNot(HaveOccurred())
		})
		It("Successfully builds an ISO from a luet channel including overlayed files", func() {
			cfg.ISO.RootFS = []string{"system/elemental", "/overlay/dir"}
			cfg.ISO.UEFI = []string{"live/efi"}
			cfg.ISO.Image = []string{"live/bootloader"}

			err := utils.MkdirAll(fs, "/overlay/dir/boot", constants.DirPerm)
			Expect(err).ShouldNot(HaveOccurred())
			_, err = fs.Create("/overlay/dir/boot/vmlinuz")
			Expect(err).ShouldNot(HaveOccurred())
			_, err = fs.Create("/overlay/dir/boot/initrd")
			Expect(err).ShouldNot(HaveOccurred())

			err = action.BuildISORun(cfg)

			Expect(luet.UnpackChannelCalled()).To(BeTrue())
			Expect(err).ShouldNot(HaveOccurred())
		})
		It("Fails if kernel or initrd is not found in rootfs", func() {
			cfg.ISO.RootFS = []string{"/local/dir"}
			cfg.ISO.UEFI = []string{"live/efi"}
			cfg.ISO.Image = []string{"live/bootloader"}

			err := utils.MkdirAll(fs, "/local/dir/boot", constants.DirPerm)
			Expect(err).ShouldNot(HaveOccurred())

			By("fails without kernel")
			err = action.BuildISORun(cfg)
			Expect(err).Should(HaveOccurred())

			By("fails without initrd")
			_, err = fs.Create("/local/dir/boot/vmlinuz")
			Expect(err).ShouldNot(HaveOccurred())
			err = action.BuildISORun(cfg)
			Expect(err).Should(HaveOccurred())
		})
		It("Fails installing rootfs sources", func() {
			cfg.ISO.RootFS = []string{"system/elemental"}
			luet.OnUnpackFromChannelError = true

			err := action.BuildISORun(cfg)
			Expect(err).Should(HaveOccurred())
			Expect(luet.UnpackChannelCalled()).To(BeTrue())
		})
		It("Fails installing uefi sources", func() {
			cfg.ISO.RootFS = []string{"elemental:latest"}
			cfg.ISO.UEFI = []string{"live/efi"}
			luet.OnUnpackFromChannelError = true

			err := action.BuildISORun(cfg)
			Expect(err).Should(HaveOccurred())
			Expect(luet.UnpackCalled()).To(BeTrue())
			Expect(luet.UnpackChannelCalled()).To(BeTrue())
		})
		It("Fails installing image sources", func() {
			cfg.ISO.RootFS = []string{"elemental:latest"}
			cfg.ISO.UEFI = []string{"registry.suse.com/custom-uefi:v0.1"}
			cfg.ISO.Image = []string{"live/bootloader"}
			luet.OnUnpackFromChannelError = true

			err := action.BuildISORun(cfg)
			Expect(err).Should(HaveOccurred())
			Expect(luet.UnpackCalled()).To(BeTrue())
			Expect(luet.UnpackChannelCalled()).To(BeTrue())
		})
		It("Fails on ISO filesystem creation", func() {
			cfg.ISO.RootFS = []string{"/local/dir"}
			cfg.ISO.UEFI = []string{"live/efi"}
			cfg.ISO.Image = []string{"live/bootloader"}

			err := utils.MkdirAll(fs, "/local/dir/boot", constants.DirPerm)
			Expect(err).ShouldNot(HaveOccurred())
			_, err = fs.Create("/local/dir/boot/vmlinuz")
			Expect(err).ShouldNot(HaveOccurred())
			_, err = fs.Create("/local/dir/boot/initrd")
			Expect(err).ShouldNot(HaveOccurred())

			runner.SideEffect = func(command string, args ...string) ([]byte, error) {
				if command == "xorriso" {
					return []byte{}, errors.New("Burn ISO error")
				}
				return []byte{}, nil
			}

			err = action.BuildISORun(cfg)

			Expect(luet.UnpackChannelCalled()).To(BeTrue())
			Expect(err).Should(HaveOccurred())
		})
	})
	Describe("Build disk", Label("disk", "build"), func() {
		BeforeEach(func() {
			cfg.RawDisk = map[string]*v1.RawDiskArchEntry{
				"x86_64": {Repositories: nil, Packages: []v1.RawDiskPackage{{Name: "what", Target: "what"}}},
			}
			cfg.Repos = []v1.Repository{{URI: "test"}}
		})
		It("Builds a raw image", func() {
			// temp dir for output, otherwise we write to .
			outputDir, _ := utils.TempDir(fs, "", "output")
			// temp dir for package files, create needed file
			filesDir, _ := utils.TempDir(fs, "", "elemental-build-disk-files")
			_ = utils.MkdirAll(fs, filepath.Join(filesDir, "root", "etc", "cos"), constants.DirPerm)
			_ = fs.WriteFile(filepath.Join(filesDir, "root", "etc", "cos", "grubenv_firstboot"), []byte(""), os.ModePerm)

			// temp dir for part files, create parts
			partsDir, _ := utils.TempDir(fs, "", "elemental-build-disk-parts")
			_ = fs.WriteFile(filepath.Join(partsDir, "rootfs.part"), []byte(""), os.ModePerm)
			_ = fs.WriteFile(filepath.Join(partsDir, "oem.part"), []byte(""), os.ModePerm)
			_ = fs.WriteFile(filepath.Join(partsDir, "efi.part"), []byte(""), os.ModePerm)

			err := action.BuildDiskRun(cfg, "raw", "x86_64", "OEM", "REC", filepath.Join(outputDir, "disk.raw"))
			Expect(err).ToNot(HaveOccurred())
			// Check that we copied all needed files to final image
			Expect(memLog.String()).To(ContainSubstring("efi.part"))
			Expect(memLog.String()).To(ContainSubstring("rootfs.part"))
			Expect(memLog.String()).To(ContainSubstring("oem.part"))
			output, err := fs.Stat(filepath.Join(outputDir, "disk.raw"))
			Expect(err).ToNot(HaveOccurred())
			// Even with empty parts, output image should never be zero due to the truncating
			// it should be exactly 20Mb(efi size) + 64Mb(oem size) + 2048Mb(recovery size) + 3Mb(hybrid boot) + 1Mb(GPT)
			partsSize := (20 + 64 + 2048 + 3 + 1) * 1024 * 1024
			Expect(output.Size()).To(BeNumerically("==", partsSize))
			// Check that mkfs commands set the label properly and copied the proper dirs
			err = runner.IncludesCmds([][]string{
				{"mkfs.ext2", "-L", "REC", "-d", "/tmp/elemental-build-disk-files/root", "/tmp/elemental-build-disk-parts/rootfs.part"},
				{"mkfs.vfat", "-n", constants.EfiLabel, "/tmp/elemental-build-disk-parts/efi.part"},
				{"mkfs.ext2", "-L", "OEM", "-d", "/tmp/elemental-build-disk-files/oem", "/tmp/elemental-build-disk-parts/oem.part"},
				// files should be copied to EFI
				{"mcopy", "-s", "-i", "/tmp/elemental-build-disk-parts/efi.part", "/tmp/elemental-build-disk-files/efi/EFI", "::EFI"},
			})
			Expect(err).ToNot(HaveOccurred())
		})
		It("Sets default labels if empty", func() {
			// temp dir for output, otherwise we write to .
			outputDir, _ := utils.TempDir(fs, "", "output")
			// temp dir for package files, create needed file
			filesDir, _ := utils.TempDir(fs, "", "elemental-build-disk-files")
			_ = utils.MkdirAll(fs, filepath.Join(filesDir, "root", "etc", "cos"), constants.DirPerm)
			_ = fs.WriteFile(filepath.Join(filesDir, "root", "etc", "cos", "grubenv_firstboot"), []byte(""), os.ModePerm)

			// temp dir for part files, create parts
			partsDir, _ := utils.TempDir(fs, "", "elemental-build-disk-parts")
			_ = fs.WriteFile(filepath.Join(partsDir, "rootfs.part"), []byte(""), os.ModePerm)
			_ = fs.WriteFile(filepath.Join(partsDir, "oem.part"), []byte(""), os.ModePerm)
			_ = fs.WriteFile(filepath.Join(partsDir, "efi.part"), []byte(""), os.ModePerm)

			err := action.BuildDiskRun(cfg, "raw", "x86_64", "", "", filepath.Join(outputDir, "disk.raw"))
			Expect(err).ToNot(HaveOccurred())
			// Check that we copied all needed files to final image
			Expect(memLog.String()).To(ContainSubstring("efi.part"))
			Expect(memLog.String()).To(ContainSubstring("rootfs.part"))
			Expect(memLog.String()).To(ContainSubstring("oem.part"))
			output, err := fs.Stat(filepath.Join(outputDir, "disk.raw"))
			Expect(err).ToNot(HaveOccurred())
			// Even with empty parts, output image should never be zero due to the truncating
			// it should be exactly 20Mb(efi size) + 64Mb(oem size) + 2048Mb(recovery size) + 3Mb(hybrid boot) + 1Mb(GPT)
			partsSize := (20 + 64 + 2048 + 3 + 1) * 1024 * 1024
			Expect(output.Size()).To(BeNumerically("==", partsSize))
			// Check that mkfs commands set the label properly and copied the proper dirs
			err = runner.IncludesCmds([][]string{
				{"mkfs.ext2", "-L", constants.RecoveryLabel, "-d", "/tmp/elemental-build-disk-files/root", "/tmp/elemental-build-disk-parts/rootfs.part"},
				{"mkfs.vfat", "-n", constants.EfiLabel, "/tmp/elemental-build-disk-parts/efi.part"},
				{"mkfs.ext2", "-L", constants.OEMLabel, "-d", "/tmp/elemental-build-disk-files/oem", "/tmp/elemental-build-disk-parts/oem.part"},
				// files should be copied to EFI
				{"mcopy", "-s", "-i", "/tmp/elemental-build-disk-parts/efi.part", "/tmp/elemental-build-disk-files/efi/EFI", "::EFI"},
			})
			Expect(err).ToNot(HaveOccurred())
		})
		It("Transforms raw image into GCE image", Label("gce"), func() {
			tmpDir, err := utils.TempDir(fs, "", "")
			defer fs.RemoveAll(tmpDir)
			Expect(err).ToNot(HaveOccurred())
			Expect(err).ToNot(HaveOccurred())
			f, err := fs.Create(filepath.Join(tmpDir, "disk.raw"))
			Expect(err).ToNot(HaveOccurred())
			// Set a non rounded size
			f.Truncate(34 * 1024 * 1024)
			f.Close()
			err = action.Raw2Gce(filepath.Join(tmpDir, "disk.raw"), fs, logger, false)
			Expect(err).ToNot(HaveOccurred())
			// Log should have the rounded size (1Gb)
			Expect(memLog.String()).To(ContainSubstring(strconv.Itoa(1 * 1024 * 1024 * 1024)))
			// Should be a tar file
			realPath, _ := fs.RawPath(tmpDir)
			Expect(dockerArchive.IsArchivePath(filepath.Join(realPath, "disk.raw.tar.gz"))).To(BeTrue())
		})
		It("Transforms raw image into Azure image", func() {
			tmpDir, err := utils.TempDir(fs, "", "")
			defer fs.RemoveAll(tmpDir)
			Expect(err).ToNot(HaveOccurred())
			Expect(err).ToNot(HaveOccurred())
			f, err := fs.Create(filepath.Join(tmpDir, "disk.raw"))
			Expect(err).ToNot(HaveOccurred())
			// write something
			_ = f.Truncate(23 * 1024 * 1024)
			_ = f.Close()
			err = action.Raw2Azure(filepath.Join(tmpDir, "disk.raw"), fs, logger, true)
			Expect(err).ToNot(HaveOccurred())
			info, err := fs.Stat(filepath.Join(tmpDir, "disk.raw.vhd"))
			Expect(err).ToNot(HaveOccurred())
			// Should have be rounded up to the next MB
			Expect(info.Size()).To(BeNumerically("==", 23*1024*1024))

			// Read the header
			f, _ = fs.Open(filepath.Join(tmpDir, "disk.raw.vhd"))
			info, _ = f.Stat()
			// Dump the header from the file into our VHDHeader
			buff := make([]byte, 512)
			_, _ = f.ReadAt(buff, info.Size()-512)
			_ = f.Close()

			header := utils.VHDHeader{}
			err = binary.Read(bytes.NewBuffer(buff[:]), binary.BigEndian, &header)
			Expect(err).ToNot(HaveOccurred())
			// Just check the fields that we know the value of, that should indicate that the header is valid
			Expect(hex.EncodeToString(header.DiskType[:])).To(Equal("00000002"))
			Expect(hex.EncodeToString(header.Features[:])).To(Equal("00000002"))
			Expect(hex.EncodeToString(header.DataOffset[:])).To(Equal("ffffffffffffffff"))
		})
		It("Transforms raw image into Azure image (really small image)", func() {
			// This tests that the resize works for extreme small images
			// Not sure if we ever will enconuter them (less than 1 Mb images?) but just in case
			tmpDir, err := utils.TempDir(fs, "", "")
			defer fs.RemoveAll(tmpDir)
			Expect(err).ToNot(HaveOccurred())
			Expect(err).ToNot(HaveOccurred())
			f, err := fs.Create(filepath.Join(tmpDir, "disk.raw"))
			Expect(err).ToNot(HaveOccurred())
			// write something
			_, _ = f.WriteString("Hi")
			_ = f.Close()
			err = action.Raw2Azure(filepath.Join(tmpDir, "disk.raw"), fs, logger, true)
			Expect(err).ToNot(HaveOccurred())
			info, err := fs.Stat(filepath.Join(tmpDir, "disk.raw"))
			Expect(err).ToNot(HaveOccurred())
			// Should be smaller than 1Mb
			Expect(info.Size()).To(BeNumerically("<", 1*1024*1024))

			info, err = fs.Stat(filepath.Join(tmpDir, "disk.raw.vhd"))
			Expect(err).ToNot(HaveOccurred())
			// Should have be rounded up to the next MB
			Expect(info.Size()).To(BeNumerically("==", 1*1024*1024))

			// Read the header
			f, _ = fs.Open(filepath.Join(tmpDir, "disk.raw.vhd"))
			info, _ = f.Stat()
			// Dump the header from the file into our VHDHeader
			buff := make([]byte, 512)
			_, _ = f.ReadAt(buff, info.Size()-512)
			_ = f.Close()

			header := utils.VHDHeader{}
			err = binary.Read(bytes.NewBuffer(buff[:]), binary.BigEndian, &header)
			Expect(err).ToNot(HaveOccurred())
			// Just check the fields that we know the value of, that should indicate that the header is valid
			Expect(hex.EncodeToString(header.DiskType[:])).To(Equal("00000002"))
			Expect(hex.EncodeToString(header.Features[:])).To(Equal("00000002"))
			Expect(hex.EncodeToString(header.DataOffset[:])).To(Equal("ffffffffffffffff"))
		})
	})
})
