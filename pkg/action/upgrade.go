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
	"github.com/rancher-sandbox/elemental/pkg/constants"
	"github.com/rancher-sandbox/elemental/pkg/elemental"
	v1 "github.com/rancher-sandbox/elemental/pkg/types/v1"
	"github.com/rancher-sandbox/elemental/pkg/utils"
	"github.com/spf13/afero"
	"k8s.io/mount-utils"
	"os"
	"path/filepath"
)

// UpgradeAction represents the struct that will run the upgrade from start to finish
type UpgradeAction struct {
	Config *v1.RunConfig
}

func NewUpgradeAction(config *v1.RunConfig) *UpgradeAction {
	return &UpgradeAction{Config: config}
}

func (u *UpgradeAction) Info(s string, args ...interface{}) {
	u.Config.Logger.Infof(s, args...)
}

func (u *UpgradeAction) Debug(s string, args ...interface{}) {
	u.Config.Logger.Debugf(s, args...)
}

func (u *UpgradeAction) Error(s string, args ...interface{}) {
	u.Config.Logger.Errorf(s, args...)
}

func upgradeHook(config *v1.RunConfig, hook string, chroot bool) error {
	if chroot {
		mountPoints := map[string]string{}

		oemDevice, err := utils.GetFullDeviceByLabel(config.Runner, config.OEMLabel, 5)
		if err == nil && oemDevice.MountPoint != "" {
			mountPoints[oemDevice.MountPoint] = "/oem"
		}

		persistentDevice, err := utils.GetFullDeviceByLabel(config.Runner, config.PersistentLabel, 5)
		if err == nil && persistentDevice.MountPoint != "" {
			mountPoints[persistentDevice.MountPoint] = "/usr/local"
		}

		return ActionChrootHook(config, hook, config.ActiveImage.MountPoint, mountPoints)
	}
	return ActionHook(config, hook)
}

func (u *UpgradeAction) Run() (err error) {
	var statePart v1.Partition
	var transitionImg string
	upgradeStateDir := constants.RunningStateDir

	cleanup := utils.NewCleanStack()
	defer func() { err = cleanup.Cleanup(err) }()

	// if upgrading the recovery we mount the state in a different place as its already mounted RO, we need to remount it
	if u.Config.RecoveryUpgrade {
		upgradeStateDir = constants.UpgradeRecoveryDir
	}

	upgradeTarget, upgradeSource := u.getTargetAndSource()

	u.Config.Logger.Infof("Upgrading %s partition", upgradeTarget)

	err = u.Config.Fs.MkdirAll(constants.UpgradeTempDir, os.ModeDir)
	if err != nil {
		u.Error("Error creating target dir %s: %s", constants.UpgradeTempDir, err)
		return err
	}
	cleanup.Push(func() error { return u.remove(constants.UpgradeTempDir) })

	if u.Config.RecoveryUpgrade {
		statePart, err = utils.GetFullDeviceByLabel(u.Config.Runner, u.Config.RecoveryLabel, 5)
		if err != nil {
			u.Error("Could not find state partition to mount with label %s", u.Config.RecoveryLabel)
			return err
		}
	} else {
		statePart, err = utils.GetFullDeviceByLabel(u.Config.Runner, u.Config.StateLabel, 5)
		if err != nil {
			u.Error("Could not find state partition to mount with label %s", u.Config.StateLabel)
			return err
		}
	}

	u.Info("Mounting state partition %s in %s", statePart.Path, upgradeStateDir)
	if exists, _ := afero.Exists(u.Config.Fs, upgradeStateDir); !exists {
		err = u.Config.Fs.MkdirAll(upgradeStateDir, os.ModeDir)

		if err != nil {
			u.Error("Error creating statedir %s: %s", upgradeStateDir, err)
			return err
		}
	}

	statePartMountOptions := []string{"remount", "rw"}

	// If we want to upgrade the active but are booting from recovery, the statedir is not mounted, so dont remount
	if !u.Config.RecoveryUpgrade && utils.BootedFrom(u.Config.Runner, u.Config.RecoveryLabel) {
		statePartMountOptions = []string{"rw"}
		cleanup.Push(func() error { return u.unmount(upgradeStateDir) })
		// Also mount oem and persistent as they are not mounted on recovery
		oemPart, err := utils.GetFullDeviceByLabel(u.Config.Runner, u.Config.OEMLabel, 5)
		if err == nil {
			err := u.Config.Fs.MkdirAll(constants.OEMDir, os.ModeDir)
			if err == nil {
				err = u.Config.Mounter.Mount(oemPart.Path, constants.OEMDir, oemPart.FS, []string{})
				if err != nil {
					u.Config.Logger.Warnf("Could not mount oem partition: %s", err)
				} else {
					cleanup.Push(func() error { return u.unmount(constants.OEMDir) })
				}
			}
		}
		persistentPart, err := utils.GetFullDeviceByLabel(u.Config.Runner, u.Config.PersistentLabel, 5)
		if err == nil {
			err := u.Config.Fs.MkdirAll(constants.PersistentDir, os.ModeDir)
			if err == nil {
				err = u.Config.Mounter.Mount(persistentPart.Path, constants.PersistentDir, persistentPart.FS, []string{})
				if err != nil {
					u.Config.Logger.Warnf("Could not mount persistent partition: %s", err)
				} else {
					cleanup.Push(func() error { return u.unmount(constants.PersistentDir) })
				}
			}
		}
	}

	// If we want to upgrade the recovery but are not booting from recovery, the stateDir is not mounted, so dont try to remount
	if u.Config.RecoveryUpgrade && !utils.BootedFrom(u.Config.Runner, u.Config.RecoveryLabel) {
		statePartMountOptions = []string{"rw"}
		cleanup.Push(func() error { return u.unmount(upgradeStateDir) })
	}

	err = u.Config.Mounter.Mount(statePart.Path, upgradeStateDir, statePart.FS, statePartMountOptions)
	if err != nil {
		u.Error("Error mounting %s: %s", upgradeStateDir, err)
		return err
	}

	if !utils.BootedFrom(u.Config.Runner, u.Config.RecoveryLabel) {
		cleanup.Push(func() error {
			return u.remount(mount.MountPoint{Device: statePart.Path, Path: upgradeStateDir, Type: statePart.FS}, "ro")
		})
	}

	// Track if recovery.squash file exists which indicates that the recovery is squash
	isSquashRecovery, _ := afero.Exists(u.Config.Fs, filepath.Join(upgradeStateDir, "cOS", constants.RecoverySquashFile))

	if isSquashRecovery {
		u.Debug("Recovery is squash")
		transitionImg = filepath.Join(upgradeStateDir, "cOS", constants.TransitionSquashFile)
	} else {
		transitionImg = filepath.Join(upgradeStateDir, "cOS", constants.TransitionImgFile)
	}

	u.Debug("Using transition img: %s", transitionImg)

	cleanup.Push(func() error { return u.remove(transitionImg) })

	// create transition.img
	img := v1.Image{
		File:       transitionImg,
		Size:       u.Config.ImgSize,
		Label:      u.Config.ActiveLabel,
		FS:         constants.LinuxImgFs,
		MountPoint: constants.UpgradeTempDir,
		RootTree:   upgradeSource.Source, // if source is a dir it will copy from here, if it's a docker img it uses Config.DockerImg IN THAT ORDER!
	}

	// If on recovery, set the label to the RecoveryLabel instead
	if utils.BootedFrom(u.Config.Runner, u.Config.RecoveryLabel) {
		img.Label = u.Config.SystemLabel
	}

	ele := elemental.NewElemental(u.Config)

	if !isSquashRecovery {
		// Only on recovery+squash we dont use the img file
		err = ele.CreateFileSystemImage(img)
		if err != nil {
			u.Error("Failed to create %s img: %s", transitionImg, err)
			return err
		}

		// mount the transition img on targetDir, so we can install the upgraded files into targetDir, and they end up on the img
		err = ele.MountImage(&img, "rw")
	}

	for _, d := range []string{"proc", "boot", "dev", "sys", "tmp", "usr/local", "oem"} {
		_ = u.Config.Fs.MkdirAll(filepath.Join(constants.UpgradeTempDir, d), os.ModeDir)
	}

	err = upgradeHook(u.Config, "before-upgrade", false)
	if err != nil {
		u.Error("Error while running hook before-upgrade: %s", err)
		return err
	}
	// Setting the activeImg to our img, tricks CopyActive into doing it anyway even if it's a recovery img
	u.Config.ActiveImage = img
	err = ele.CopyActive(upgradeSource)
	if err != nil {
		u.Error("Error copying active: %s", err)
		return err
	}
	// Selinux relabel
	// In the original script, any errors are ignored
	_, _ = u.Config.Runner.Run("chmod", "755", constants.UpgradeTempDir)
	_ = ele.SelinuxRelabel(constants.UpgradeTempDir, false)

	// Only run rebrand on non recovery+squash
	err = upgradeHook(u.Config, "after-upgrade-chroot", true)
	if err != nil {
		u.Error("Error running hook after-upgrade-chroot: %s", err)
		return err
	}

	// Load the os-release file from the new upgraded system
	osRelease, err := utils.LoadEnvFile(u.Config.Fs, filepath.Join(constants.UpgradeTempDir, "etc", "os-release"))
	// override grub vars with the new system vars
	u.Config.GrubDefEntry = osRelease["GRUB_ENTRY_NAME"]

	err = ele.Rebrand()

	if err != nil {
		u.Error("Error running rebrand: %s", err)
		return err
	}
	err = upgradeHook(u.Config, "after-upgrade", false)

	if err != nil {
		u.Error("Error running hook after-upgrade: %s", err)
		return err
	}

	if !isSquashRecovery {
		// Copy is done, unmount transition.img
		err = ele.UnmountImage(&img)
		if err != nil {
			u.Error("Error unmounting %s: %s", img.MountPoint, err)
			return err
		}
	}

	// If booted from active and not updating recovery, backup active into passive
	if utils.BootedFrom(u.Config.Runner, u.Config.ActiveLabel) && !u.Config.RecoveryUpgrade {
		// backup current active.img to passive.img before overwriting the active.img
		u.Info("Backing up current active image")
		source := filepath.Join(upgradeStateDir, "cOS", constants.ActiveImgFile)
		destination := filepath.Join(upgradeStateDir, "cOS", constants.PassiveImgFile)
		u.Info("Moving %s to %s", source, destination)
		_, err := u.Config.Runner.Run("mv", "-f", source, destination)
		if err != nil {
			u.Error("Failed to move %s to %s: %s", source, destination, err)
			return err
		}
		u.Info("Finished moving %s to %s", source, destination)
		// Label the image to passive!
		out, err := u.Config.Runner.Run("tune2fs", "-L", u.Config.PassiveLabel, destination)
		if err != nil {
			u.Error("Error while labeling the passive image %s: %s", destination, err)
			u.Debug("Error while labeling the passive image %s, command output: %s", out)
			return err
		}
		_, _ = u.Config.Runner.Run("sync")
	}
	// Final step, move the newly updated img/squash into the proper place
	finalDestination := filepath.Join(upgradeStateDir, "cOS", fmt.Sprintf("%s.img", upgradeTarget))

	if isSquashRecovery {
		finalDestination = filepath.Join(upgradeStateDir, "cOS", constants.RecoverySquashFile)
		options := constants.GetDefaultSquashfsOptions()
		u.Info("Creating %s", constants.RecoverySquashFile)
		err = utils.CreateSquashFS(u.Config.Runner, u.Config.Logger, constants.UpgradeTempDir, transitionImg, options)
		if err != nil {
			return err
		}
	}

	u.Info("Moving %s to %s", transitionImg, finalDestination)
	_, err = u.Config.Runner.Run("mv", "-f", transitionImg, finalDestination)
	if err != nil {
		u.Error("Failed to move %s to %s: %s", transitionImg, finalDestination, err)
		return err
	}
	u.Info("Finished moving %s to %s", transitionImg, finalDestination)

	_, _ = u.Config.Runner.Run("sync")

	u.Info("Upgrade completed")

	// Do not reboot/poweroff on cleanup errors
	err = cleanup.Cleanup(err)
	if err != nil {
		return err
	}
	if u.Config.Reboot {
		u.Info("Rebooting in 5 seconds")
		return utils.Reboot(u.Config.Runner, 5)
	} else if u.Config.PowerOff {
		u.Info("Shutting down in 5 seconds")
		return utils.Shutdown(u.Config.Runner, 5)
	}
	return err
}

// unmount attempts to unmount the given path. Does nothing if not mounted
func (u *UpgradeAction) unmount(path string) error {
	if notMounted, _ := u.Config.Mounter.IsLikelyNotMountPoint(path); !notMounted {
		u.Debug("[Cleanup] Unmounting %s", path)
		return u.Config.Mounter.Unmount(path)
	}
	return nil
}

// remove attempts to remove the given path. Does nothing if it doesn't exist
func (u *UpgradeAction) remove(path string) error {
	if exists, _ := afero.Exists(u.Config.Fs, path); exists {
		u.Debug("[Cleanup] Removing %s", path)
		return u.Config.Fs.RemoveAll(path)
	}
	return nil
}

// remount attemps to remount the given mountpoint with the provided options. Does nothing if not mounted
func (u *UpgradeAction) remount(m mount.MountPoint, opts ...string) error {
	if notMounted, _ := u.Config.Mounter.IsLikelyNotMountPoint(m.Path); !notMounted {
		u.Debug("[Cleanup] Remount %s", m.Path)
		return u.Config.Mounter.Mount(m.Device, m.Path, m.Type, append([]string{"remount"}, opts...))
	}
	return nil
}

// getTargetAndSource finds our the target and source for the upgrade
func (u *UpgradeAction) getTargetAndSource() (string, v1.InstallUpgradeSource) {
	upgradeSource := v1.InstallUpgradeSource{Source: constants.UpgradeSource, IsChannel: true}
	upgradeTarget := constants.UpgradeActive

	// if upgrade_recovery==true then it upgrades only the recovery
	// if upgrade_recovery==false then it upgrades only the active
	// default is active
	if u.Config.RecoveryUpgrade {
		u.Debug("Upgrading recovery")
		upgradeTarget = constants.UpgradeRecovery
	}

	// if channel_upgrades==true then it picks the default image from /etc/cos-upgrade-image
	// this means, it gets the UPGRADE_IMAGE(default system/cos) from the luet repo configured on the system
	if u.Config.ChannelUpgrades {
		u.Debug("Source is channel-upgrades")
		upgradeSource.Source = u.Config.UpgradeImage // Loaded from /etc/cos-upgrade-image
	} else {
		// if channel_upgrades==false then
		// if docker-image -> upgrade from image directly, ignores release_channel and pulls the given image directly
		if u.Config.DockerImg != "" {
			u.Debug("Source is docker image: %s", u.Config.DockerImg)
			upgradeSource = v1.InstallUpgradeSource{Source: u.Config.DockerImg, IsDocker: true}
		}
		// if directory -> upgrade from dir directly, ignores release_channel and uses the given directory
		if u.Config.DirectoryUpgrade != "" {
			u.Debug("Source is directory: %s", u.Config.DirectoryUpgrade)
			upgradeSource = v1.InstallUpgradeSource{Source: u.Config.DirectoryUpgrade, IsDir: true}
		}
	}
	return upgradeTarget, upgradeSource
}
