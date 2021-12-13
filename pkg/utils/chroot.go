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

package utils

import (
	"fmt"
	v1 "github.com/rancher-sandbox/elemental-cli/pkg/types/v1"
	"github.com/spf13/afero"
	mountUtils "k8s.io/mount-utils"
	"os"
	"strings"
)

type Chroot struct {
	path          string
	defaultMounts []string
	mounter       mountUtils.Interface
	runner        v1.Runner
	syscall       v1.SyscallInterface
	fs            afero.Fs
	// TODO: Should chroot just accept a RunConfig??
}

// NewChroot returns a *Chroot with the proper options set, allows overriding the runner/syscall/fs by using WithXX methods under options.go
func NewChroot(path string, opts ...ChrootOptions) *Chroot {
	c := &Chroot{
		path:          path,
		defaultMounts: []string{"/dev", "/dev/pts", "/proc", "/sys"},
		runner:        &v1.RealRunner{},
		syscall:       &v1.RealSyscall{},
		fs:            afero.NewOsFs(),
	}

	for _, o := range opts {
		err := o(c)
		if err != nil {
			return nil
		}
	}
	// Check if we passed a mounter and set the default otherwise
	// We do it here because the mounter will call systemd to check if it's running in a systemd enabled system
	// And that can lead to asking for elevation permissions, even when passing a different mounter
	if c.mounter == nil {
		c.mounter = mountUtils.New(path)
	}
	return c
}

// Prepare will mount the defaultMounts as bind mounts in order to set up the chroot properly
func (c Chroot) Prepare() error {
	mountOptions := []string{"bind"}
	for _, mnt := range c.defaultMounts {
		mountPoint := fmt.Sprintf("%s%s", strings.TrimSuffix(c.path, "/"), mnt)
		err := c.fs.Mkdir(mountPoint, 0644)
		// TODO: Should probably check if they are mounted??
		err = c.mounter.Mount(mnt, mountPoint, "bind", mountOptions)
		if err != nil {
			return err
		}
	}
	return nil
}

// Close will unmount the default mounts set by Prepare
func (c Chroot) Close() error {
	for _, mnt := range c.defaultMounts {
		err := c.mounter.Unmount(fmt.Sprintf("%s%s", strings.TrimSuffix(c.path, "/"), mnt))
		if err != nil {
			return err
		}
	}
	return nil
}

// Run executes a command inside a chroot
func (c Chroot) Run(command string, args ...string) ([]byte, error) {
	var out []byte
	var err error
	// Store current dir
	oldRootF, err := os.Open("/") // Cant use afero here because doesnt support chdir done below
	defer oldRootF.Close()
	if err != nil {
		fmt.Printf("Cant open /")
		return out, err
	}
	err = c.Prepare()
	if err != nil {
		fmt.Printf("Cant mount default mounts")
		return nil, err
	}
	err = c.syscall.Chroot(c.path)
	if err != nil {
		fmt.Printf("Cant chroot %s", c.path)
		return out, err
	}
	// run commands in the chroot
	out, err = c.runner.Run(command, args...)
	if err != nil {
		fmt.Printf("Cant run command on chroot")
		return out, err
	}
	// Restore to old dir
	err = oldRootF.Chdir()
	if err != nil {
		fmt.Printf("Cant change to old dir")
		return out, err
	}
	err = c.syscall.Chroot(".")
	if err != nil {
		fmt.Printf("Cant chroot back to oldir")
		return out, err
	}
	return out, err
}
