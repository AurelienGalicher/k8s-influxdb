// +build linux

package fs

import (
	"revision.aeip.apigee.net/spatel/k8s-influxdb/influxdb/Godeps/_workspace/src/github.com/opencontainers/runc/libcontainer/cgroups"
	"revision.aeip.apigee.net/spatel/k8s-influxdb/influxdb/Godeps/_workspace/src/github.com/opencontainers/runc/libcontainer/configs"
)

type NetPrioGroup struct {
}

func (s *NetPrioGroup) Name() string {
	return "net_prio"
}

func (s *NetPrioGroup) Apply(d *cgroupData) error {
	dir, err := d.join("net_prio")
	if err != nil && !cgroups.IsNotFound(err) {
		return err
	}

	if err := s.Set(dir, d.config); err != nil {
		return err
	}

	return nil
}

func (s *NetPrioGroup) Set(path string, cgroup *configs.Cgroup) error {
	for _, prioMap := range cgroup.NetPrioIfpriomap {
		if err := writeFile(path, "net_prio.ifpriomap", prioMap.CgroupString()); err != nil {
			return err
		}
	}

	return nil
}

func (s *NetPrioGroup) Remove(d *cgroupData) error {
	return removePath(d.path("net_prio"))
}

func (s *NetPrioGroup) GetStats(path string, stats *cgroups.Stats) error {
	return nil
}
