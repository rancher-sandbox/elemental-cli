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

package v1_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/rancher-sandbox/elemental/pkg/types/v1"
	v1mock "github.com/rancher-sandbox/elemental/tests/mocks"
)

// unit test stolen from yip
var _ = Describe("Syscall", Label("types", "syscall", "chroot"), func() {
	It("Calling chroot on the real syscall should fail", func() {
		r := v1.RealSyscall{}
		err := r.Chroot("/tmp/")
		// We need elevated privs to chroot so this should fail
		Expect(err).ToNot(BeNil())
	})

	It("Calling chroot on the fake syscall should not fail", func() {
		r := v1mock.FakeSyscall{}
		err := r.Chroot("/tmp/")
		// We need elevated privs to chroot so this should fail
		Expect(err).To(BeNil())
	})

	It("Calling chroot on the real syscall should not fail", Label("root"), func() {
		r := v1.RealSyscall{}
		err := r.Chroot("/")
		Expect(err).ToNot(HaveOccurred())
	})
})
