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

package mocks

import (
	"errors"

	luetTypes "github.com/mudler/luet/pkg/api/core/types"
)

type FakeLuet struct {
	OnUnpackError            bool
	OnUnpackFromChannelError bool
	unpackCalled             bool
	unpackFromChannelCalled  bool
}

func NewFakeLuet() *FakeLuet {
	return &FakeLuet{}
}

func (l *FakeLuet) Unpack(target string, image string, local bool) error {
	l.unpackCalled = true
	if l.OnUnpackError {
		return errors.New("Luet install error")
	}
	return nil
}

func (l *FakeLuet) UnpackFromChannel(target string, pkg string) error {
	l.unpackFromChannelCalled = true
	if l.OnUnpackFromChannelError {
		return errors.New("Luet install error")
	}
	return nil
}

func (l FakeLuet) UnpackCalled() bool {
	return l.unpackCalled
}

func (l FakeLuet) UnpackChannelCalled() bool {
	return l.unpackFromChannelCalled
}

func (l FakeLuet) OverrideConfig(config *luetTypes.LuetConfig) {}
