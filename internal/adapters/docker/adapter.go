// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package docker

import "autohost-agent/internal/domain"

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Install() error                         { return Install() }
func (a *Adapter) StopApp(appName domain.AppName) error   { return StopApp(appName) }
func (a *Adapter) StartApp(appName domain.AppName) error  { return StartApp(appName) }
func (a *Adapter) RemoveApp(appName domain.AppName) error { return RemoveApp(appName) }
func (a *Adapter) GetAppStatus(appName domain.AppName) (string, error) {
	return GetAppStatus(appName)
}
func (a *Adapter) DockerInstalled() bool       { return DockerInstalled() }
func (a *Adapter) CreateDockerNetwork() error  { return CreateDockerNetwork() }
func (a *Adapter) AddUserToDockerGroup() error { return AddUserToDockerGroup() }
