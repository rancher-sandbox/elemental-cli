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

package action

import (
	"errors"
	cnst "github.com/rancher-sandbox/elemental/pkg/constants"
	"github.com/rancher-sandbox/elemental/pkg/elemental"
	"github.com/rancher-sandbox/elemental/pkg/types/v1"
	"github.com/rancher-sandbox/elemental/pkg/utils"
	"github.com/spf13/afero"
	"path/filepath"
)

func resetHook(config *v1.RunConfig, hook string, chroot bool) error {
	if chroot {
		extraMounts := map[string]string{}
		persistent := config.Partitions.GetByName(cnst.PersistentPartName)
		if persistent != nil {
			extraMounts[persistent.MountPoint] = "/usr/local"
		}
		oem := config.Partitions.GetByName(cnst.OEMPartName)
		if oem != nil {
			extraMounts[oem.MountPoint] = "/oem"
		}
		return ActionChrootHook(config, hook, config.ActiveImage.MountPoint, extraMounts)
	}
	return ActionHook(config, hook)
}

// ResetSetup will set installation parameters according to
// the given configuration flags
func ResetSetup(config *v1.RunConfig) error {
	if !utils.BootedFrom(config.Runner, cnst.RecoverySquashFile) &&
		!utils.BootedFrom(config.Runner, config.SystemLabel) {
		return errors.New("Reset can only be called from the recovery system")
	}

	SetupLuet(config)

	var rootTree string
	// TODO Properly set image souce here
	// TODO execute rootTree sanity checks
	if config.Directory != "" {
		rootTree = config.Directory
	} else if config.DockerImg != "" {
		rootTree = config.DockerImg
	} else if utils.BootedFrom(config.Runner, cnst.RecoverySquashFile) {
		rootTree = cnst.IsoBaseTree
	} else {
		rootTree = filepath.Join(cnst.RunningStateDir, "cOS", cnst.RecoveryImgFile)
	}

	efiExists, _ := afero.Exists(config.Fs, cnst.EfiDevice)

	if efiExists {
		partEfi, err := utils.GetFullDeviceByLabel(config.Runner, cnst.EfiLabel, 1)
		if err != nil {
			return err
		}
		if partEfi.MountPoint == "" {
			partEfi.MountPoint = cnst.EfiDir
		}
		partEfi.Name = cnst.EfiPartName
		config.Partitions = append(config.Partitions, &partEfi)
	}

	// Only add it if it exists, not a hard requirement
	partOEM, err := utils.GetFullDeviceByLabel(config.Runner, cnst.OEMLabel, 1)
	if err == nil {
		if partOEM.MountPoint == "" {
			partOEM.MountPoint = cnst.OEMDir
		}
		partOEM.Name = cnst.OEMPartName
		config.Partitions = append(config.Partitions, &partOEM)
	} else {
		config.Logger.Warnf("No OEM partition found")
	}

	partState, err := utils.GetFullDeviceByLabel(config.Runner, cnst.StateLabel, 1)
	if err != nil {
		return err
	}
	if partState.MountPoint == "" {
		partState.MountPoint = cnst.StateDir
	}
	partState.Name = cnst.StatePartName
	config.Partitions = append(config.Partitions, &partState)
	config.Target = partState.Disk

	// Only add it if it exists, not a hard requirement
	partPersistent, err := utils.GetFullDeviceByLabel(config.Runner, cnst.PersistentLabel, 1)
	if err == nil {
		if partPersistent.MountPoint == "" {
			partPersistent.MountPoint = cnst.PersistentDir
		}
		partPersistent.Name = cnst.PersistentPartName
		config.Partitions = append(config.Partitions, &partPersistent)
	} else {
		config.Logger.Warnf("No Persistent partition found")
	}

	config.ActiveImage = v1.Image{
		Label:      config.ActiveLabel,
		Size:       cnst.ImgSize,
		File:       filepath.Join(partState.MountPoint, "cOS", cnst.ActiveImgFile),
		FS:         cnst.LinuxImgFs,
		RootTree:   rootTree,
		MountPoint: cnst.ActiveDir,
	}

	return nil
}

// ResetRun will reset the cos system to by following several steps
func ResetRun(config *v1.RunConfig) (err error) {
	ele := elemental.NewElemental(config)
	cleanup := utils.NewCleanStack()
	defer func() { err = cleanup.Cleanup(err) }()

	err = resetHook(config, cnst.BeforeResetHook, false)
	if err != nil {
		return err
	}

	// Unmount partitions if any is already mounted before formatting
	err = ele.UnmountPartitions()
	if err != nil {
		return err
	}

	// Reformat state partition
	err = ele.FormatPartition(config.Partitions.GetByName(cnst.StatePartName))
	if err != nil {
		return err
	}
	// Reformat persistent partitions
	if config.ResetPersistent {
		persistent := config.Partitions.GetByName(cnst.PersistentPartName)
		if persistent != nil {
			err = ele.FormatPartition(persistent)
			if err != nil {
				return err
			}
		}
		oem := config.Partitions.GetByName(cnst.OEMPartName)
		if oem != nil {
			err = ele.FormatPartition(oem)
			if err != nil {
				return err
			}
		}
	}

	// Mount configured partitions
	err = ele.MountPartitions()
	if err != nil {
		return err
	}
	cleanup.Push(func() error { return ele.UnmountPartitions() })

	// install Active
	// TODO all this logic should be part` of the CopyImage(img *v1.Image) refactor up to
	// TODO setting source should be part of ResetSetup
	source := v1.InstallUpgradeSource{Source: config.ActiveImage.RootTree}
	if config.Directory != "" {
		source.IsDir = true
	} else if config.DockerImg != "" {
		source.IsDocker = true
	} else if config.ActiveImage.RootTree != "" {
		source.IsDir = true
	} else {
		source.Source = filepath.Join(cnst.RunningStateDir, "cOS", cnst.RecoveryImgFile)
		source.IsFile = true
	}

	if !source.IsFile {
		err = ele.CreateFileSystemImage(config.ActiveImage)
		if err != nil {
			return err
		}

		//mount file system image
		err = ele.MountImage(&config.ActiveImage, "rw")
		if err != nil {
			return err
		}
		cleanup.Push(func() error { return ele.UnmountImage(&config.ActiveImage) })
	}
	err = ele.CopyActive(source)
	if err != nil {
		return err
	}
	if source.IsFile {
		err = ele.MountImage(&config.ActiveImage, "rw")
		if err != nil {
			return err
		}
		cleanup.Push(func() error { return ele.UnmountImage(&config.ActiveImage) })
	}
	// TODO: here ends the CopyImage(img *v1.Image)

	// install grub
	grub := utils.NewGrub(config)
	err = grub.Install()
	if err != nil {
		return err
	}
	// Relabel SELinux
	_ = ele.SelinuxRelabel(cnst.ActiveDir, false)

	err = resetHook(config, cnst.AfterResetChrootHook, true)
	if err != nil {
		return err
	}

	// Unmount active image
	err = ele.UnmountImage(&config.ActiveImage)
	if err != nil {
		return err
	}

	// install Passive
	err = ele.CopyPassive()
	if err != nil {
		return err
	}

	err = resetHook(config, cnst.AfterResetHook, false)
	if err != nil {
		return err
	}

	// installation rebrand (only grub for now)
	err = ele.Rebrand()
	if err != nil {
		return err
	}

	// Do not reboot/poweroff on cleanup errors
	err = cleanup.Cleanup(err)
	if err != nil {
		return err
	}

	// Reboot, poweroff or nothing
	if config.Reboot {
		config.Logger.Infof("Rebooting in 5 seconds")
		return utils.Reboot(config.Runner, 5)
	} else if config.PowerOff {
		config.Logger.Infof("Shutting down in 5 seconds")
		return utils.Shutdown(config.Runner, 5)
	}
	return err
}
