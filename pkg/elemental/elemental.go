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

package elemental

import (
	"errors"
	"fmt"
	cnst "github.com/rancher-sandbox/elemental-cli/pkg/constants"
	part "github.com/rancher-sandbox/elemental-cli/pkg/partitioner"
	v1 "github.com/rancher-sandbox/elemental-cli/pkg/types/v1"
	"github.com/rancher-sandbox/elemental-cli/pkg/utils"
	"github.com/spf13/afero"
	"github.com/zloylos/grsync"
	"io"
	"os"
	"strings"
)

// Elemental is the struct meant to self-contain most utils and actions related to Elemental, like installing or applying selinux
type Elemental struct {
	config *v1.RunConfig
}

func NewElemental(config *v1.RunConfig) *Elemental {
	return &Elemental{
		config: config,
	}
}

// PartitionAndFormatDevice creates a new empty partition table on target disk
// and applies the configured disk layout by creating and formatting all
// required partitions
func (c *Elemental) PartitionAndFormatDevice(disk *part.Disk) error {
	c.config.Logger.Infof("Partitioning device...")

	err := c.createPTableAndFirmwarePartitions(disk)
	if err != nil {
		return err
	}

	if c.config.PartTable == v1.GPT && c.config.PartLayout != "" {
		return c.config.CloudInitRunner.Run(cnst.PartStage, c.config.PartLayout)
	}

	return c.createDataPartitions(disk)
}

func (c *Elemental) createPTableAndFirmwarePartitions(disk *part.Disk) error {
	errCMsg := "Failed creating %s partition"
	errFMsg := "Failed formatting partition: %s"

	c.config.Logger.Debugf("Creating partition table...")
	out, err := disk.NewPartitionTable(c.config.PartTable)
	if err != nil {
		c.config.Logger.Errorf("Failed creating new partition table: %s", out)
		return err
	}

	if c.config.PartTable == v1.GPT && c.config.BootFlag == v1.ESP {
		c.config.Logger.Debugf("Creating EFI partition...")
		efiNum, err := disk.AddPartition(cnst.EfiSize, cnst.EfiFs, cnst.EfiPLabel, v1.ESP)
		if err != nil {
			c.config.Logger.Errorf(errCMsg, cnst.EfiPLabel)
			return err
		}
		out, err = disk.FormatPartition(efiNum, cnst.EfiFs, cnst.EfiLabel)
		if err != nil {
			c.config.Logger.Errorf(errFMsg, out)
			return err
		}
	} else if c.config.PartTable == v1.GPT && c.config.BootFlag == v1.BIOS {
		c.config.Logger.Debugf("Creating Bios partition...")
		_, err = disk.AddPartition(cnst.BiosSize, cnst.BiosFs, cnst.BiosPLabel, v1.BIOS)
		if err != nil {
			c.config.Logger.Errorf(errCMsg, cnst.BiosPLabel)
			return err
		}
	}
	return nil
}

func (c *Elemental) createDataPartitions(disk *part.Disk) error {
	errCMsg := "Failed creating %s partition"
	errFMsg := "Failed formatting partition: %s"

	stateFlags := []string{}
	if c.config.PartTable == v1.MSDOS {
		stateFlags = append(stateFlags, v1.BOOT)
	}
	oemNum, err := disk.AddPartition(c.config.OEMPart.Size, c.config.OEMPart.FS, c.config.OEMPart.PLabel)
	if err != nil {
		c.config.Logger.Errorf(errCMsg, c.config.OEMPart.PLabel)
		return err
	}
	stateNum, err := disk.AddPartition(c.config.StatePart.Size, c.config.StatePart.FS, c.config.StatePart.PLabel, stateFlags...)
	if err != nil {
		c.config.Logger.Errorf(errCMsg, c.config.StatePart.PLabel)
		return err
	}
	recoveryNum, err := disk.AddPartition(c.config.RecoveryPart.Size, c.config.RecoveryPart.FS, c.config.RecoveryPart.PLabel)
	if err != nil {
		c.config.Logger.Errorf(errCMsg, cnst.RecoveryPLabel)
		return err
	}
	persistentNum, err := disk.AddPartition(c.config.PersistentPart.Size, c.config.PersistentPart.FS, c.config.PersistentPart.PLabel)
	if err != nil {
		c.config.Logger.Errorf(errCMsg, c.config.PersistentPart.PLabel)
		return err
	}

	out, err := disk.FormatPartition(oemNum, c.config.OEMPart.FS, c.config.OEMPart.Label)
	if err != nil {
		c.config.Logger.Errorf(errFMsg, out)
		return err
	}
	out, err = disk.FormatPartition(stateNum, c.config.StatePart.FS, c.config.StatePart.Label)
	if err != nil {
		c.config.Logger.Errorf(errFMsg, out)
		return err
	}
	out, err = disk.FormatPartition(recoveryNum, c.config.RecoveryPart.FS, c.config.RecoveryPart.Label)
	if err != nil {
		c.config.Logger.Errorf(errFMsg, out)
		return err
	}
	out, err = disk.FormatPartition(persistentNum, c.config.PersistentPart.FS, c.config.PersistentPart.Label)
	if err != nil {
		c.config.Logger.Errorf(errFMsg, out)
		return err
	}
	return nil
}

// CopyCos will rsync from config.source to config.target
func (c *Elemental) CopyCos() error {
	c.config.Logger.Infof("Copying cOS..")
	// Make sure the values have a / at the end.
	var source, target string
	if strings.HasSuffix(c.config.Source, "/") == false {
		source = fmt.Sprintf("%s/", c.config.Source)
	} else {
		source = c.config.Source
	}

	if strings.HasSuffix(c.config.Target, "/") == false {
		target = fmt.Sprintf("%s/", c.config.Target)
	} else {
		target = c.config.Target
	}
	// 1 - rsync all the system from source to target
	task := grsync.NewTask(
		source,
		target,
		grsync.RsyncOptions{
			Quiet:   false,
			Archive: true,
			XAttrs:  true,
			ACLs:    true,
			Exclude: []string{"mnt", "proc", "sys", "dev", "tmp"},
		},
	)

	if err := task.Run(); err != nil {
		return err
	}
	c.config.Logger.Infof("Finished copying cOS..")
	return nil
}

// CopyCloudConfig will check if there is a cloud init in the config and store it on the target
func (c *Elemental) CopyCloudConfig() error {
	if c.config.CloudInit != "" {
		customConfig := fmt.Sprintf("%s/oem/99_custom.yaml", c.config.Target)
		c.config.Logger.Infof("Trying to copy cloud config file %s to %s", c.config.CloudInit, customConfig)

		if err :=
			c.GetUrl(c.config.CloudInit, customConfig); err != nil {
			return err
		}

		if err := c.config.Fs.Chmod(customConfig, os.ModePerm); err != nil {
			return err
		}
		c.config.Logger.Infof("Finished copying cloud config file %s to %s", c.config.CloudInit, customConfig)
	}
	return nil
}

// SelinuxRelabel will relabel the system if it finds the binary and the context
func (c *Elemental) SelinuxRelabel(raiseError bool) error {
	var err error

	contextFile := fmt.Sprintf("%s/etc/selinux/targeted/contexts/files/file_contexts", c.config.Target)

	_, err = c.config.Fs.Stat(contextFile)
	contextExists := err == nil

	if utils.CommandExists("setfiles") && contextExists {
		_, err = c.config.Runner.Run("setfiles", "-r", c.config.Target, contextFile, c.config.Target)
	}

	// In the original code this can error out and we dont really care
	// I guess that to maintain backwards compatibility we have to do the same, we dont care if it raises an error
	// but we still add the possibility to return an error if we want to change it in the future to be more strict?
	if raiseError && err != nil {
		return err
	} else {
		return nil
	}
}

// CheckNoFormat will make sure that if we set the no format option, the system doesnt already contain a cos system
// by checking the active/passive labels. If they are set then we check if we have the force flag, which means that we
// don't care and proceed to overwrite
func (c *Elemental) CheckNoFormat() error {
	if c.config.NoFormat {
		// User asked for no format, lets check if there is already those labeled partitions in the disk
		for _, label := range []string{c.config.ActiveLabel, c.config.PassiveLabel} {
			found, err := utils.FindLabel(c.config.Runner, label)
			if err != nil {
				return err
			}
			if found != "" {
				if c.config.Force {
					msg := fmt.Sprintf("Forcing overwrite of existing partitions due to `force` flag")
					c.config.Logger.Infof(msg)
					return nil
				} else {
					msg := fmt.Sprintf("There is already an active deployment in the system, use '--force' flag to overwrite it")
					c.config.Logger.Error(msg)
					return errors.New(msg)
				}
			}
		}
	}
	return nil
}

// GetRecoveryDir will return the proper dir for the recovery, depending on if we are booting from squashfs or not
func (c *Elemental) GetRecoveryDir() string {
	if c.BootedFromSquash() {
		return cnst.RecoveryDirSquash
	} else {
		return cnst.RecoveryDir
	}
}

// BootedFromSquash will check if we are booting from squashfs
func (c Elemental) BootedFromSquash() bool {
	if utils.BootedFrom(c.config.Runner, cnst.RecoveryLabel) {
		return true
	}
	return false
}

// GetIso will check if iso flag is set and if true will try to:
// download the iso to a temp file
// and mount the iso file as loop,
// and modify the IsoMnt var to point to the newly mounted dir
func (c *Elemental) GetIso() error {
	if c.config.Iso != "" {
		tmpDir, err := afero.TempDir(c.config.Fs, "", "elemental")
		if err != nil {
			return err
		}
		tmpFile := fmt.Sprintf("%s/cOs.iso", tmpDir)
		err = c.GetUrl(c.config.Iso, tmpFile)
		if err != nil {
			defer c.config.Fs.RemoveAll(tmpDir)
			return err
		}
		tmpIsoMount, err := afero.TempDir(c.config.Fs, "", "elemental-iso-mounted-")
		if err != nil {
			defer c.config.Fs.RemoveAll(tmpDir)
			return err
		}
		var mountOptions []string
		c.config.Logger.Infof("Mounting iso %s into %s", tmpFile, tmpIsoMount)
		err = c.config.Mounter.Mount(tmpFile, tmpIsoMount, "loop", mountOptions)
		if err != nil {
			defer c.config.Fs.RemoveAll(tmpDir)
			defer c.config.Fs.RemoveAll(tmpIsoMount)
			return err
		}
		// Store the new mounted dir into IsoMnt, so we can use it down the line
		c.config.IsoMnt = tmpIsoMount
		return nil
	}
	return nil
}

// GetUrl is a simple method that will try to get an url to a destination, no matter if its an http url, ftp, tftp or a file
func (c *Elemental) GetUrl(url string, destination string) error {
	var source []byte
	var err error

	switch {
	case strings.HasPrefix(url, "http"), strings.HasPrefix(url, "ftp"), strings.HasPrefix(url, "tftp"):
		c.config.Logger.Infof("Downloading from %s to %s", url, destination)
		resp, err := c.config.Client.Get(url)
		if err != nil {
			return err
		}
		_, err = resp.Body.Read(source)
		defer resp.Body.Close()
	default:
		c.config.Logger.Infof("Copying from %s to %s", url, destination)
		source, err = afero.ReadFile(c.config.Fs, url)
		if err != nil {
			return err
		}
	}

	err = afero.WriteFile(c.config.Fs, destination, source, os.ModePerm)
	if err != nil {
		return err
	}
	return nil
}

// CopyRecovery will
// Check if we are booting from squash -> false? return
// true? -> :
// mkdir -p RECOVERYDIR
// mount RECOVERY into RECOVERYDIR
// mkdir -p  RECOVERYDIR/cOS
// if squash -> cp -a RECOVERYSQUASHFS to RECOVERYDIR/cOS/recovery.squashfs
// if not -> cp -a STATEDIR/cOS/active.img to RECOVERYDIR/cOS/recovery.img
// Where:
// RECOVERYDIR is GetRecoveryDir
// ISOMNT is /run/initramfs/live by default, can be set to a different dir if COS_INSTALL_ISO_URL is set
// RECOVERYSQUASHFS is $ISOMNT/recovery.squashfs
// RECOVERY is GetDeviceByLabel(cnst.RecoveryLabel)
// either is get from the system if NoFormat is enabled (searching for label COS_RECOVERY) or is a newly generated partition
func (c *Elemental) CopyRecovery() error {
	var err error
	if !c.BootedFromSquash() {
		return nil
	}
	recoveryDir := c.GetRecoveryDir()
	recoveryDirCos := fmt.Sprintf("%s/cOS", recoveryDir)
	recoveryDirCosSquashTarget := fmt.Sprintf("%s/cOS/%s", recoveryDir, cnst.RecoverySquashFile)
	isoMntCosSquashSource := fmt.Sprintf("%s/%s", c.config.IsoMnt, cnst.RecoverySquashFile)
	imgCosSource := fmt.Sprintf("%s/cOS/%s", c.config.StateDir, cnst.ActiveImgFile)
	imgCosTarget := fmt.Sprintf("%s/cOS/%s", recoveryDir, cnst.RecoveryImgFile)

	err = c.config.Fs.MkdirAll(recoveryDir, 0644)
	if err != nil {
		return err
	}
	var mountOptions []string
	// Get CURRENT recovery device
	// This can be an existing one (--no-format flag) or a new one done by the partitioner
	recovery, err := c.GetDeviceByLabel(c.config.GetRecoveryLabel())
	if err != nil {
		return err
	}
	err = c.config.Mounter.Mount(recoveryDir, recovery, "auto", mountOptions)
	if err != nil {
		return err
	}
	err = c.config.Fs.MkdirAll(recoveryDirCos, 0644)
	if err != nil {
		return err
	}
	if exists, _ := afero.Exists(c.config.Fs, isoMntCosSquashSource); exists {
		c.config.Logger.Infof("Copying squashfs..")
		sourceSquash, err := c.config.Fs.Open(isoMntCosSquashSource)
		if err != nil {
			return err
		}
		defer sourceSquash.Close()
		targetSquash, err := c.config.Fs.Create(recoveryDirCosSquashTarget)
		if err != nil {
			return err
		}
		defer targetSquash.Close()
		_, err = io.Copy(targetSquash, sourceSquash)
		if err != nil {
			return err
		}
	} else {
		c.config.Logger.Infof("Copying image file..")
		sourceImg, err := c.config.Fs.Open(imgCosSource)
		if err != nil {
			return err
		}
		defer sourceImg.Close()
		targetImg, err := c.config.Fs.Create(imgCosTarget)
		if err != nil {
			return err
		}
		defer targetImg.Close()
		_, err = io.Copy(targetImg, sourceImg)
		if err != nil {
			return err
		}
		_, err = c.config.Runner.Run("sync")
		if err != nil {
			return err
		}
		_, err = c.config.Runner.Run("tune2fs", "-L", c.config.GetSystemLabel(), imgCosSource)
		if err != nil {
			return err
		}
	}
	c.config.Logger.Infof("Recovery copied")
	return nil
}

// GetDeviceByLabel will try to return the device that matches the given label
func (c *Elemental) GetDeviceByLabel(label string) (string, error) {
	out, err := c.config.Runner.Run("blkid", "-t", fmt.Sprintf("LABEL=%s", label), "-o", "device")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(out)) == "" {
		return "", errors.New("no device found")
	}
	return strings.TrimSpace(string(out)), nil
}
