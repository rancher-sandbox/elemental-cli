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

package elemental

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	cnst "github.com/rancher-sandbox/elemental/pkg/constants"
	"github.com/rancher-sandbox/elemental/pkg/partitioner"
	v1 "github.com/rancher-sandbox/elemental/pkg/types/v1"
	"github.com/rancher-sandbox/elemental/pkg/utils"
)

// Elemental is the struct meant to self-contain most utils and actions related to Elemental, like installing or applying selinux
type Elemental struct {
	config *v1.Config
}

func NewElemental(config *v1.Config) *Elemental {
	return &Elemental{
		config: config,
	}
}

// FormatPartition will format an already existing partition
func (e *Elemental) FormatPartition(part *v1.Partition, opts ...string) error {
	e.config.Logger.Infof("Formatting '%s' partition", part.Name)
	return partitioner.FormatDevice(e.config.Runner, part.Path, part.FS, part.Label, opts...)
}

// PartitionAndFormatDevice creates a new empty partition table on target disk
// and applies the configured disk layout by creating and formatting all
// required partitions
func (e *Elemental) PartitionAndFormatDevice(i *v1.InstallSpec) error {
	disk := partitioner.NewDisk(
		i.Target,
		partitioner.WithRunner(e.config.Runner),
		partitioner.WithFS(e.config.Fs),
		partitioner.WithLogger(e.config.Logger),
	)

	if !disk.Exists() {
		e.config.Logger.Errorf("Disk %s does not exist", i.Target)
		return fmt.Errorf("disk %s does not exist", i.Target)
	}

	err := i.Partitions.SetFirmwarePartitions(i.Firmware, i.PartTable)
	if err != nil {
		return err
	}

	e.config.Logger.Infof("Partitioning device...")
	out, err := disk.NewPartitionTable(i.PartTable)
	if err != nil {
		e.config.Logger.Errorf("Failed creating new partition table: %s", out)
		return err
	}

	parts := i.Partitions.PartitionsByInstallOrder()

	return e.createPartitions(disk, parts)
}

func (e *Elemental) createAndFormatPartition(disk *partitioner.Disk, part *v1.Partition) error {
	e.config.Logger.Debugf("Adding partition %s", part.Name)
	num, err := disk.AddPartition(part.Size, part.FS, part.Name, part.Flags...)
	if err != nil {
		e.config.Logger.Errorf("Failed creating %s partition", part.Name)
		return err
	}
	partDev, err := disk.FindPartitionDevice(num)
	if err != nil {
		return err
	}
	if part.FS != "" {
		e.config.Logger.Debugf("Formatting partition with label %s", part.Label)
		err = partitioner.FormatDevice(e.config.Runner, partDev, part.FS, part.Label)
		if err != nil {
			e.config.Logger.Errorf("Failed formatting partition %s", part.Name)
			return err
		}
	} else {
		e.config.Logger.Debugf("Wipe file system on %s", part.Name)
		err = disk.WipeFsOnPartition(partDev)
		if err != nil {
			e.config.Logger.Errorf("Failed to wipe filesystem of partition %s", partDev)
			return err
		}
	}
	part.Path = partDev
	return nil
}

func (e *Elemental) createPartitions(disk *partitioner.Disk, parts v1.PartitionList) error {
	for _, part := range parts {
		err := e.createAndFormatPartition(disk, part)
		if err != nil {
			return err
		}
	}
	return nil
}

// MountPartitions mounts configured partitions. Partitions with an unset mountpoint are not mounted.
// Note umounts must be handled by caller logic.
func (e Elemental) MountPartitions(parts v1.PartitionList) error {
	e.config.Logger.Infof("Mounting disk partitions")
	var err error

	for _, part := range parts {
		if part.MountPoint != "" {
			err = e.MountPartition(part, "rw")
			if err != nil {
				_ = e.UnmountPartitions(parts)
				return err
			}
		}
	}

	return err
}

// UnmountPartitions unmounts configured partitiosn. Partitions with an unset mountpoint are not unmounted.
func (e Elemental) UnmountPartitions(parts v1.PartitionList) error {
	e.config.Logger.Infof("Unmounting disk partitions")
	var err error
	errMsg := ""
	failure := false

	// If there is an early error we still try to unmount other partitions
	for _, part := range parts {
		if part.MountPoint != "" {
			err = e.UnmountPartition(part)
			if err != nil {
				errMsg += fmt.Sprintf("Failed to unmount %s\n", part.MountPoint)
				failure = true
			}
		}
	}
	if failure {
		return errors.New(errMsg)
	}
	return nil
}

// MountPartition mounts a partition with the given mount options
func (e Elemental) MountPartition(part *v1.Partition, opts ...string) error {
	e.config.Logger.Debugf("Mounting partition %s", part.Label)
	err := utils.MkdirAll(e.config.Fs, part.MountPoint, cnst.DirPerm)
	if err != nil {
		return err
	}
	if part.Path == "" {
		// Lets error out only after 10 attempts to find the device
		device, err := utils.GetDeviceByLabel(e.config.Runner, part.Label, 10)
		if err != nil {
			e.config.Logger.Errorf("Could not find a device with label %s", part.Label)
			return err
		}
		part.Path = device
	}
	err = e.config.Mounter.Mount(part.Path, part.MountPoint, "auto", opts)
	if err != nil {
		e.config.Logger.Errorf("Failed mounting device %s with label %s", part.Path, part.Label)
		return err
	}
	return nil
}

// UnmountPartition unmounts the given partition or does nothing if not mounted
func (e Elemental) UnmountPartition(part *v1.Partition) error {
	if mnt, _ := utils.IsMounted(e.config, part); !mnt {
		e.config.Logger.Debugf("Not unmounting partition, %s doesn't look like mountpoint", part.MountPoint)
		return nil
	}
	e.config.Logger.Debugf("Unmounting partition %s", part.Label)
	return e.config.Mounter.Unmount(part.MountPoint)
}

// MountImage mounts an image with the given mount options
func (e Elemental) MountImage(img *v1.Image, opts ...string) error {
	e.config.Logger.Debugf("Mounting image %s", img.Label)
	err := utils.MkdirAll(e.config.Fs, img.MountPoint, cnst.DirPerm)
	if err != nil {
		return err
	}
	out, err := e.config.Runner.Run("losetup", "--show", "-f", img.File)
	if err != nil {
		return err
	}
	loop := strings.TrimSpace(string(out))
	err = e.config.Mounter.Mount(loop, img.MountPoint, "auto", opts)
	if err != nil {
		_, _ = e.config.Runner.Run("losetup", "-d", loop)
		return err
	}
	img.LoopDevice = loop
	return nil
}

// UnmountImage unmounts the given image or does nothing if not mounted
func (e Elemental) UnmountImage(img *v1.Image) error {
	// Using IsLikelyNotMountPoint seams to be safe as we are not checking
	// for bind mounts here
	if notMnt, _ := e.config.Mounter.IsLikelyNotMountPoint(img.MountPoint); notMnt {
		e.config.Logger.Debugf("Not unmounting image, %s doesn't look like mountpoint", img.MountPoint)
		return nil
	}

	e.config.Logger.Debugf("Unmounting image %s", img.Label)
	err := e.config.Mounter.Unmount(img.MountPoint)
	if err != nil {
		return err
	}
	_, err = e.config.Runner.Run("losetup", "-d", img.LoopDevice)
	img.LoopDevice = ""
	return err
}

// CreateFileSystemImage creates the image file for config.target
func (e Elemental) CreateFileSystemImage(img *v1.Image) error {
	e.config.Logger.Infof("Creating file system image %s", img.File)
	err := utils.MkdirAll(e.config.Fs, filepath.Dir(img.File), cnst.DirPerm)
	if err != nil {
		return err
	}
	actImg, err := e.config.Fs.Create(img.File)
	if err != nil {
		return err
	}

	err = actImg.Truncate(int64(img.Size * 1024 * 1024))
	if err != nil {
		actImg.Close()
		_ = e.config.Fs.RemoveAll(img.File)
		return err
	}
	err = actImg.Close()
	if err != nil {
		_ = e.config.Fs.RemoveAll(img.File)
		return err
	}

	mkfs := partitioner.NewMkfsCall(img.File, img.FS, img.Label, e.config.Runner)
	_, err = mkfs.Apply()
	if err != nil {
		_ = e.config.Fs.RemoveAll(img.File)
		return err
	}
	return nil
}

// DeployImage will deploay the given image into the target. This method
// creates the filesystem image file, mounts it and unmounts it as needed.
func (e *Elemental) DeployImage(img *v1.Image, leaveMounted bool) error {
	var err error

	target := img.MountPoint
	if !img.Source.IsFile() {
		if img.FS != cnst.SquashFs {
			err = e.CreateFileSystemImage(img)
			if err != nil {
				return err
			}

			err = e.MountImage(img, "rw")
			if err != nil {
				return err
			}
		} else {
			target = utils.GetTempDir(e.config, "")
			err := utils.MkdirAll(e.config.Fs, target, cnst.DirPerm)
			if err != nil {
				return err
			}
			defer e.config.Fs.RemoveAll(target) // nolint:errcheck
		}
	} else {
		target = img.File
	}
	err = e.DumpSource(target, img.Source)
	if err != nil {
		_ = e.UnmountImage(img)
		return err
	}
	if !img.Source.IsFile() {
		err = utils.CreateDirStructure(e.config.Fs, target)
		if err != nil {
			return err
		}
		if img.FS == cnst.SquashFs {
			opts := append(cnst.GetDefaultSquashfsOptions(), e.config.SquashFsCompressionConfig...)
			err = utils.CreateSquashFS(e.config.Runner, e.config.Logger, target, img.File, opts)
			if err != nil {
				return err
			}
		}
	} else if img.Label != "" && img.FS != cnst.SquashFs {
		_, err = e.config.Runner.Run("tune2fs", "-L", img.Label, img.File)
		if err != nil {
			e.config.Logger.Errorf("Failed to apply label %s to $s", img.Label, img.File)
			_ = e.config.Fs.Remove(img.File)
			return err
		}
	}
	if leaveMounted && img.Source.IsFile() {
		err = e.MountImage(img, "rw")
		if err != nil {
			return err
		}
	}
	if !leaveMounted {
		err = e.UnmountImage(img)
		if err != nil {
			return err
		}
	}
	return nil
}

// DumpSource sets the image data according to the image source type
func (e *Elemental) DumpSource(target string, imgSrc *v1.ImageSource) error { // nolint:gocyclo
	e.config.Logger.Infof("Copying %s source...", imgSrc.Value())
	var err error

	if imgSrc.IsDocker() {
		if e.config.Cosign {
			e.config.Logger.Infof("Running cosing verification for %s", imgSrc.Value())
			out, err := utils.CosignVerify(
				e.config.Fs, e.config.Runner, imgSrc.Value(),
				e.config.CosignPubKey, v1.IsDebugLevel(e.config.Logger),
			)
			if err != nil {
				e.config.Logger.Errorf("Cosign verification failed: %s", out)
				return err
			}
		}
		err = e.config.Luet.Unpack(img.MountPoint, img.Source.Value(), e.config.LocalImage)
		if err != nil {
			return err
		}
	} else if imgSrc.IsDir() {
		excludes := []string{"/mnt", "/proc", "/sys", "/dev", "/tmp", "/host", "/run"}
		err = utils.SyncData(e.config.Fs, imgSrc.Value(), target, excludes...)
		if err != nil {
			return err
		}
	} else if imgSrc.IsChannel() {
		err = e.config.Luet.UnpackFromChannel(target, imgSrc.Value())
		if err != nil {
			return err
		}
	} else if imgSrc.IsFile() {
		err := utils.MkdirAll(e.config.Fs, filepath.Dir(target), cnst.DirPerm)
		if err != nil {
			return err
		}
		err = utils.CopyFile(e.config.Fs, imgSrc.Value(), target)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf("unknown image source type")
	}
	e.config.Logger.Infof("Finished copying %s into %s", imgSrc.Value(), target)
	return nil
}

// CopyCloudConfig will check if there is a cloud init in the config and store it on the target
func (e *Elemental) CopyCloudConfig(cloudInit string) (err error) {
	if cloudInit != "" {
		customConfig := filepath.Join(cnst.OEMDir, "99_custom.yaml")
		err = utils.GetSource(e.config, cloudInit, customConfig)
		if err != nil {
			return err
		}
		if err = e.config.Fs.Chmod(customConfig, cnst.FilePerm); err != nil {
			return err
		}
		e.config.Logger.Infof("Finished copying cloud config file %s to %s", cloudInit, customConfig)
	}
	return nil
}

// SelinuxRelabel will relabel the system if it finds the binary and the context
func (e *Elemental) SelinuxRelabel(rootDir string, raiseError bool) error {
	var err error

	contextFile := filepath.Join(rootDir, "/etc/selinux/targeted/contexts/files/file_contexts")

	_, err = e.config.Fs.Stat(contextFile)
	contextExists := err == nil

	if utils.CommandExists("setfiles") && contextExists {
		_, err = e.config.Runner.Run("setfiles", "-r", rootDir, contextFile, rootDir)
	}

	// In the original code this can error out and we dont really care
	// I guess that to maintain backwards compatibility we have to do the same, we dont care if it raises an error
	// but we still add the possibility to return an error if we want to change it in the future to be more strict?
	if raiseError && err != nil {
		return err
	}
	return nil
}

// CheckActiveDeployment returns true if at least one of the provided filesystem labels is found within the system
func (e *Elemental) CheckActiveDeployment(labels []string) bool {
	e.config.Logger.Infof("Checking for active deployment")

	for _, label := range labels {
		found, _ := utils.GetDeviceByLabel(e.config.Runner, label, 1)
		if found != "" {
			e.config.Logger.Debug("there is already an active deployment in the system")
			return true
		}
	}
	return false
}

// GetIso will try to:
// download the iso into a temporary folder and mount the iso file as loop
// in cnst.DownloadedIsoMnt
func (e *Elemental) GetIso(iso string) (tmpDir string, err error) {
	//TODO support ISO download in persistent storage?
	tmpDir, err = utils.TempDir(e.config.Fs, "", "elemental")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = e.config.Fs.RemoveAll(tmpDir)
		}
	}()

	isoMnt := filepath.Join(tmpDir, "iso")
	rootfsMnt := filepath.Join(tmpDir, "rootfs")

	tmpFile := filepath.Join(tmpDir, "cOs.iso")
	err = utils.GetSource(e.config, iso, tmpFile)
	if err != nil {
		return "", err
	}
	err = utils.MkdirAll(e.config.Fs, isoMnt, cnst.DirPerm)
	if err != nil {
		return "", err
	}
	e.config.Logger.Infof("Mounting iso %s into %s", tmpFile, isoMnt)
	err = e.config.Mounter.Mount(tmpFile, isoMnt, "auto", []string{"loop"})
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = e.config.Mounter.Unmount(isoMnt)
		}
	}()

	e.config.Logger.Infof("Mounting squashfs image from iso into %s", rootfsMnt)
	err = utils.MkdirAll(e.config.Fs, rootfsMnt, cnst.DirPerm)
	if err != nil {
		return "", err
	}
	err = e.config.Mounter.Mount(filepath.Join(isoMnt, cnst.IsoRootFile), rootfsMnt, "auto", []string{})
	return tmpDir, err
}

// UpdateSourcesFormDownloadedISO checks a downaloaded and mounted ISO in workDir and updates the active and recovery image
// descriptions to use the squashed rootfs from the downloaded ISO.
func (e Elemental) UpdateSourcesFormDownloadedISO(workDir string, activeImg *v1.Image, recoveryImg *v1.Image) error {
	rootfsMnt := filepath.Join(workDir, "rootfs")
	isoMnt := filepath.Join(workDir, "iso")

	if activeImg != nil {
		activeImg.Source = v1.NewDirSrc(rootfsMnt)
	}
	if recoveryImg != nil {
		squashedImgSource := filepath.Join(isoMnt, cnst.RecoverySquashFile)
		if exists, _ := utils.Exists(e.config.Fs, squashedImgSource); exists {
			recoveryImg.Source = v1.NewFileSrc(squashedImgSource)
			recoveryImg.FS = cnst.SquashFs
		} else if activeImg != nil {
			recoveryImg.Source = v1.NewFileSrc(activeImg.File)
			recoveryImg.FS = cnst.LinuxImgFs
			// Only update label if unset, it could happen if the host is running form another ISO.
			if recoveryImg.Label == "" {
				recoveryImg.Label = cnst.SystemLabel
			}
		} else {
			return fmt.Errorf("can't set recovery image from ISO, source image is missing")
		}
	}
	return nil
}

// Sets the default_meny_entry value in RunConfig.GrubOEMEnv file at in
// State partition mountpoint.
func (e Elemental) SetDefaultGrubEntry(mountPoint string, defaultEntry string) error {
	if defaultEntry == "" {
		e.config.Logger.Debug("unset grub default entry")
		return nil
	}
	grub := utils.NewGrub(e.config)
	return grub.SetPersistentVariables(
		filepath.Join(mountPoint, cnst.GrubOEMEnv),
		map[string]string{"default_menu_entry": defaultEntry},
	)
}
