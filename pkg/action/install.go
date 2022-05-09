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
	cnst "github.com/rancher-sandbox/elemental/pkg/constants"
	"github.com/rancher-sandbox/elemental/pkg/elemental"
	v1 "github.com/rancher-sandbox/elemental/pkg/types/v1"
	"github.com/rancher-sandbox/elemental/pkg/utils"
)

func (i *InstallAction) installHook(hook string, chroot bool) error {
	if chroot {
		extraMounts := map[string]string{}
		persistent, ok := i.spec.Partitions[cnst.PersistentPartName]
		if ok {
			extraMounts[persistent.MountPoint] = "/usr/local"
		}
		oem, ok := i.spec.Partitions[cnst.OEMPartName]
		if ok {
			extraMounts[oem.MountPoint] = "/oem"
		}
		return ChrootHook(&i.cfg.Config, hook, i.cfg.Strict, i.spec.ActiveImg.MountPoint, extraMounts, i.cfg.CloudInitPaths...)
	}
	return Hook(&i.cfg.Config, hook, i.cfg.Strict, i.cfg.CloudInitPaths...)
}

type InstallAction struct {
	cfg  *v1.RunConfigNew
	spec *v1.InstallSpec
}

func NewInstallAction(cfg *v1.RunConfigNew, spec *v1.InstallSpec) *InstallAction {
	return &InstallAction{cfg: cfg, spec: spec}
}

// InstallRun will install the system from a given configuration
func (i InstallAction) Run() (err error) { //nolint:gocyclo
	e := elemental.NewElemental(&i.cfg.Config)
	cleanup := utils.NewCleanStack()
	defer func() { err = cleanup.Cleanup(err) }()

	err = i.installHook(cnst.BeforeInstallHook, false)
	if err != nil {
		return err
	}

	// Set installation sources from a downloaded ISO
	if i.spec.Iso != "" {
		tmpDir, err := e.GetIso(i.spec.Iso)
		if err != nil {
			return err
		}
		cleanup.Push(func() error { return i.cfg.Fs.RemoveAll(tmpDir) })
		e.UpdateSourcesFormDownloadedISO(tmpDir, &i.spec.ActiveImg, &i.spec.RecoveryImg)
	}

	// Check no-format flag
	if i.spec.NoFormat {
		// Check force flag against current device
		labels := []string{i.spec.ActiveImg.Label, i.spec.RecoveryImg.Label}
		if e.CheckActiveDeployment(labels) && !i.spec.Force {
			return fmt.Errorf("use `force` flag to run an installation over the current running deployment")
		}
	} else {
		// Partition device
		err = e.PartitionAndFormatDevice(i.spec)
		if err != nil {
			return err
		}
	}

	err = e.MountPartitions(i.spec.Partitions.OrderedByMountPointPartitions(false))
	if err != nil {
		return err
	}
	cleanup.Push(func() error {
		return e.UnmountPartitions(i.spec.Partitions.OrderedByMountPointPartitions(true))
	})

	// Deploy active image
	err = e.DeployImage(&i.spec.ActiveImg, true)
	if err != nil {
		return err
	}
	cleanup.Push(func() error { return e.UnmountImage(&i.spec.ActiveImg) })

	// Copy cloud-init if any
	err = e.CopyCloudConfig(i.spec.CloudInit)
	if err != nil {
		return err
	}
	// Install grub
	grub := utils.NewGrub(&i.cfg.Config)
	err = grub.Install(
		i.spec.Target,
		i.spec.ActiveImg.MountPoint,
		i.spec.Partitions[constants.StatePartName].MountPoint,
		i.spec.GrubConf,
		i.spec.GrubTty,
		i.spec.Firmware == v1.EFI,
	)
	if err != nil {
		return err
	}
	// Relabel SELinux
	_ = e.SelinuxRelabel(cnst.ActiveDir, false)

	err = i.installHook(cnst.AfterInstallChrootHook, true)
	if err != nil {
		return err
	}

	// Unmount active image
	err = e.UnmountImage(&i.spec.ActiveImg)
	if err != nil {
		return err
	}
	// Install Recovery
	err = e.DeployImage(&i.spec.RecoveryImg, false)
	if err != nil {
		return err
	}
	// Install Passive
	err = e.DeployImage(&i.spec.PassiveImg, false)
	if err != nil {
		return err
	}

	err = i.installHook(cnst.AfterInstallHook, false)
	if err != nil {
		return err
	}

	// Installation rebrand (only grub for now)
	statePart, ok := i.spec.Partitions[cnst.StatePartName]
	if !ok {
		return fmt.Errorf("failed to set default grub2 entry, no state partition found")
	}
	err = e.SetDefaultGrubEntry(statePart.MountPoint, i.spec.GrubDefEntry)
	if err != nil {
		return err
	}

	// Do not reboot/poweroff on cleanup errors
	err = cleanup.Cleanup(err)
	if err != nil {
		return err
	}

	// If we want to eject the cd, create the required executable so the cd is ejected at shutdown
	if i.cfg.EjectCD && utils.BootedFrom(i.cfg.Runner, "cdroot") {
		i.cfg.Logger.Infof("Writing eject script")
		err = i.cfg.Fs.WriteFile("/usr/lib/systemd/system-shutdown/eject", []byte(cnst.EjectScript), 0744)
		if err != nil {
			i.cfg.Logger.Warnf("Could not write eject script, cdrom wont be ejected automatically: %s", err)
		}
	}

	// Reboot, poweroff or nothing
	if i.cfg.Reboot {
		i.cfg.Logger.Infof("Rebooting in 5 seconds")
		return utils.Reboot(i.cfg.Runner, 5)
	} else if i.cfg.PowerOff {
		i.cfg.Logger.Infof("Shutting down in 5 seconds")
		return utils.Shutdown(i.cfg.Runner, 5)
	}
	return err
}
