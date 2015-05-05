package fs

import (
	"github.com/docker/libcontainer/cgroups"
	"github.com/docker/libcontainer/configs"
)

type NetClsGroup struct {
}

func (s *NetClsGroup) Apply(d *data) error {
	dir, err := d.join("net_cls")
	if err != nil {
		return err
	}

	if err := s.Set(dir, d.c); err != nil {
		return err
	}

	return nil
}

func (s *NetClsGroup) Set(path string, cgroup *configs.Cgroup) error {
	return nil
}

func (s *NetClsGroup) Remove(d *data) error {
	return removePath(d.path("net_cls"))
}

func (s *NetClsGroup) GetStats(path string, stats *cgroups.Stats) error {
	return nil
}
