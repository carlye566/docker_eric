// generated code - DO NOT EDIT

package volumedrivers

import (
	"errors"
	"strings"
	"github.com/docker/docker/volume"
	"github.com/Sirupsen/logrus"
	"runtime/debug"
)

type client interface {
	Call(string, interface{}, interface{}) error
}

type volumeDriverProxy struct {
	client
}

type volumeDriverProxyCreateRequest struct {
	Name string
	Size string
}

type volumeDriverProxyCreateResponse struct {
	Err string
}

func (pp *volumeDriverProxy) Create(name string) (err error) {
	var (
		req volumeDriverProxyCreateRequest
		ret volumeDriverProxyCreateResponse
	)

	nm := name
	if strings.Contains(nm, volume.DefaultCephRbdSizeTag) {
		createInfos := strings.Split(nm, volume.DefaultCephRbdSizeTag)
		req.Name = createInfos[0]
		req.Size = createInfos[1]
		logrus.Infof("create volume %s with tag size %s", createInfos[0], createInfos[1])
	} else {
		req.Name = name
		req.Size = volume.DefaultCephRbdSize
		logrus.Infof("create volume %s without tag size %s", name, volume.DefaultCephRbdSize)
	}

	if err = pp.Call("VolumeDriver.Create", req, &ret); err != nil {
		return
	}

	if ret.Err != "" {
		err = errors.New(ret.Err)
	}

	return
}

type volumeDriverProxyRemoveRequest struct {
	Name string
}

type volumeDriverProxyRemoveResponse struct {
	Err string
}

func (pp *volumeDriverProxy) Remove(name string) (err error) {
	var (
		req volumeDriverProxyRemoveRequest
		ret volumeDriverProxyRemoveResponse
	)

	debug.PrintStack()
	name = volume.FiterCephSizeTagofVolumeName(name)
	req.Name = name
	if err = pp.Call("VolumeDriver.Remove", req, &ret); err != nil {
		return
	}

	if ret.Err != "" {
		err = errors.New(ret.Err)
	}

	return
}

type volumeDriverProxyPathRequest struct {
	Name string
}

type volumeDriverProxyPathResponse struct {
	Mountpoint string
	Err        string
}

func (pp *volumeDriverProxy) Path(name string) (mountpoint string, err error) {
	var (
		req volumeDriverProxyPathRequest
		ret volumeDriverProxyPathResponse
	)

	name = volume.FiterCephSizeTagofVolumeName(name)
	req.Name = name
	if err = pp.Call("VolumeDriver.Path", req, &ret); err != nil {
		return
	}

	mountpoint = ret.Mountpoint

	if ret.Err != "" {
		err = errors.New(ret.Err)
	}

	return
}

type volumeDriverProxyMountRequest struct {
	Name string
}

type volumeDriverProxyMountResponse struct {
	Mountpoint string
	Err        string
}

func (pp *volumeDriverProxy) Mount(name string) (mountpoint string, err error) {
	var (
		req volumeDriverProxyMountRequest
		ret volumeDriverProxyMountResponse
	)

	debug.PrintStack()
	name = volume.FiterCephSizeTagofVolumeName(name)
	req.Name = name
	if err = pp.Call("VolumeDriver.Mount", req, &ret); err != nil {
		return
	}

	mountpoint = ret.Mountpoint

	if ret.Err != "" {
		err = errors.New(ret.Err)
	}

	return
}

type volumeDriverProxyUnmountRequest struct {
	Name string
}

type volumeDriverProxyUnmountResponse struct {
	Err string
}

func (pp *volumeDriverProxy) Unmount(name string) (err error) {
	var (
		req volumeDriverProxyUnmountRequest
		ret volumeDriverProxyUnmountResponse
	)

	debug.PrintStack()
	name = volume.FiterCephSizeTagofVolumeName(name)
	req.Name = name
	if err = pp.Call("VolumeDriver.Unmount", req, &ret); err != nil {
		return
	}

	if ret.Err != "" {
		err = errors.New(ret.Err)
	}

	return
}
