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
	"fmt"
	"path/filepath"

	"github.com/rancher-sandbox/elemental/pkg/constants"
	"github.com/rancher-sandbox/elemental/pkg/elemental"
	v1 "github.com/rancher-sandbox/elemental/pkg/types/v1"
	"github.com/rancher-sandbox/elemental/pkg/utils"
)

// UpgradeAction represents the struct that will run the upgrade from start to finish
type UpgradeAction struct {
	config *v1.RunConfig
	spec   *v1.UpgradeSpec
}

func NewUpgradeAction(config *v1.RunConfig, spec *v1.UpgradeSpec) *UpgradeAction {
	return &UpgradeAction{config: config, spec: spec}
}

func (u UpgradeAction) Info(s string, args ...interface{}) {
	u.config.Logger.Infof(s, args...)
}

func (u UpgradeAction) Debug(s string, args ...interface{}) {
	u.config.Logger.Debugf(s, args...)
}

func (u UpgradeAction) Error(s string, args ...interface{}) {
	u.config.Logger.Errorf(s, args...)
}

func (u UpgradeAction) upgradeHook(hook string, chroot bool) error {
	u.Info("Applying '%s' hook", hook)
	if chroot {
		mountPoints := map[string]string{}

		oemDevice, ok := u.spec.Partitions[constants.OEMPartName]
		if ok && oemDevice.MountPoint != "" {
			mountPoints[oemDevice.MountPoint] = "/oem" //nolint:goconst
		}

		persistentDevice, ok := u.spec.Partitions[constants.PersistentPartName]
		if ok && persistentDevice.MountPoint != "" {
			mountPoints[persistentDevice.MountPoint] = "/usr/local" //nolint:goconst
		}

		return ChrootHook(&u.config.Config, hook, u.config.Strict, u.spec.ActiveImg.MountPoint, mountPoints, u.config.CloudInitPaths...)
	}
	return Hook(&u.config.Config, hook, u.config.Strict, u.config.CloudInitPaths...)
}

func (u *UpgradeAction) Run() (err error) {
	var mountPart *v1.Partition
	var ok bool
	var upgradeImg v1.Image
	var finalImageFile string

	cleanup := utils.NewCleanStack()
	defer func() { err = cleanup.Cleanup(err) }()

	e := elemental.NewElemental(&u.config.Config)

	if u.spec.RecoveryUpgrade {
		mountPart, ok = u.spec.Partitions[constants.RecoveryPartName]
		if !ok || mountPart.MountPoint == "" {
			return fmt.Errorf("unset recovery partition")
		}
		upgradeImg = u.spec.RecoveryImg
		if upgradeImg.FS == constants.SquashFs {
			finalImageFile = filepath.Join(mountPart.MountPoint, "cOS", constants.RecoverySquashFile)
		} else {
			finalImageFile = filepath.Join(mountPart.MountPoint, "cOS", constants.RecoveryImgFile)
		}
	} else {
		mountPart, ok = u.spec.Partitions[constants.StatePartName]
		if !ok || mountPart.MountPoint == "" {
			return fmt.Errorf("unset state partition")
		}
		upgradeImg = u.spec.ActiveImg
		finalImageFile = filepath.Join(mountPart.MountPoint, "cOS", constants.ActiveImgFile)
	}

	u.Info("mounting %s partition as rw", mountPart.Name)
	if mnt, _ := utils.IsMounted(&u.config.Config, mountPart); mnt {
		err = e.MountPartition(mountPart, "remount", "rw")
		if err != nil {
			u.Error("failed mounting %s partition: %v", mountPart.Name, err)
			return err
		}
	} else {
		err = e.MountPartition(mountPart, "rw")
		if err != nil {
			u.Error("failed mounting %s partition: %v", mountPart.Name, err)
			return err
		}
		cleanup.Push(func() error { return e.UnmountPartition(mountPart) })
	}

	// Cleanup transition image file before leaving
	cleanup.Push(func() error { return u.remove(upgradeImg.File) })

	// Recovery does not mount persistent, so try to mount it. Ignore errors, as its not mandatory.
	persistentPart, ok := u.spec.Partitions[constants.PersistentPartName]
	if ok {
		if mnt, _ := utils.IsMounted(&u.config.Config, persistentPart); !mnt {
			u.Debug("mounting persistent partition")
			err := e.MountPartition(persistentPart, "rw")
			if err != nil {
				u.config.Logger.Warn("could not mount persistent partition")
			} else {
				cleanup.Push(func() error { return e.UnmountPartition(persistentPart) })
			}
		}
	}

	// WARNING this changed the order in which this is applied, now it is before mounting/preparing image area as in install/reset
	err = u.upgradeHook("before-upgrade", false)
	if err != nil {
		u.Error("Error while running hook before-upgrade: %s", err)
		return err
	}

	u.Info("deploying image %s to %s", upgradeImg.Source.Value(), upgradeImg.File)
	err = e.DeployImage(&upgradeImg, true)
	if err != nil {
		u.Error("Failed deploying image to file %s", upgradeImg.File)
		return err
	}
	cleanup.Push(func() error { return e.UnmountImage(&upgradeImg) })

	// Selinux relabel
	// Doesn't make sense to relabel a readonly filesystem
	if upgradeImg.FS != constants.SquashFs {
		// In the original script, any errors are ignored
		_ = e.SelinuxRelabel(upgradeImg.MountPoint, false)
	}

	err = u.upgradeHook("after-upgrade-chroot", true)
	if err != nil {
		u.Error("Error running hook after-upgrade-chroot: %s", err)
		return err
	}

	// Only apply rebrand stage for system upgrades
	if !u.spec.RecoveryUpgrade {
		u.Info("rebranding")
		osRelease, _ := utils.LoadEnvFile(u.config.Config.Fs, filepath.Join(mountPart.MountPoint, "etc", "os-release"))

		grubDefEntry, ok := osRelease["GRUB_ENTRY_NAME"]
		if ok {
			err = e.SetDefaultGrubEntry(mountPart.MountPoint, grubDefEntry)
			if err != nil {
				u.Error("failed setting default entry")
				return err
			}
		} else {
			u.Debug("no grub default entry configured")
		}
	}

	err = u.upgradeHook("after-upgrade", false)
	if err != nil {
		u.Error("Error running hook after-upgrade: %s", err)
		return err
	}

	err = e.UnmountImage(&upgradeImg)
	if err != nil {
		u.Error("failed unmounting transition image")
		return err
	}

	// If not upgrading recovery, backup active into passive
	if !u.spec.RecoveryUpgrade {
		//TODO this step could be part of elemental package
		// backup current active.img to passive.img before overwriting the active.img
		u.Info("Backing up current active image")
		source := filepath.Join(mountPart.MountPoint, "cOS", constants.ActiveImgFile)
		destination := filepath.Join(mountPart.MountPoint, "cOS", constants.PassiveImgFile)
		u.Info("Moving %s to %s", source, destination)
		_, err := u.config.Runner.Run("mv", "-f", source, destination)
		if err != nil {
			u.Error("Failed to move %s to %s: %s", source, destination, err)
			return err
		}
		u.Info("Finished moving %s to %s", source, destination)
		// Label the image to passive!
		out, err := u.config.Runner.Run("tune2fs", "-L", u.spec.PassiveLabel, destination)
		if err != nil {
			u.Error("Error while labeling the passive image %s: %s", destination, err)
			u.Debug("Error while labeling the passive image %s, command output: %s", out)
			return err
		}
		_, _ = u.config.Runner.Run("sync")
	}

	u.Info("Moving %s to %s", upgradeImg.File, finalImageFile)
	_, err = u.config.Runner.Run("mv", "-f", upgradeImg.File, finalImageFile)
	if err != nil {
		u.Error("Failed to move %s to %s: %s", upgradeImg.File, finalImageFile, err)
		return err
	}
	u.Info("Finished moving %s to %s", upgradeImg.File, finalImageFile)

	_, _ = u.config.Runner.Run("sync")

	u.Info("Upgrade completed")

	// Do not reboot/poweroff on cleanup errors
	err = cleanup.Cleanup(err)
	if err != nil {
		return err
	}
	if u.config.Reboot {
		u.Info("Rebooting in 5 seconds")
		return utils.Reboot(u.config.Runner, 5)
	} else if u.config.PowerOff {
		u.Info("Shutting down in 5 seconds")
		return utils.Shutdown(u.config.Runner, 5)
	}
	return err
}

// remove attempts to remove the given path. Does nothing if it doesn't exist
func (u *UpgradeAction) remove(path string) error {
	if exists, _ := utils.Exists(u.config.Fs, path); exists {
		u.Debug("[Cleanup] Removing %s", path)
		return u.config.Fs.RemoveAll(path)
	}
	return nil
}
