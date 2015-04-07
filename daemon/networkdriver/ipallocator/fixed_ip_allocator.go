package ipallocator

import (
	"errors"
	"net"
	"sync"

	"github.com/Sirupsen/logrus"
)

var (
	fixed_ip_lock  = sync.Mutex{}
	fixedIP        = make(map[string]string)

	FakeContainerId             = "FAKE_CONTAINER_ID"
	ErrFixedIPAlreadyAllocated  = errors.New("requested fix ip is already allocated")
)

func RegisterFixedIP(ips []net.IP) error {
	if ips == nil || len(ips) == 0 {
		return nil
	}
	fixed_ip_lock.Lock()
	defer fixed_ip_lock.Unlock()
	for _, ip := range ips {
		key := ip.String()
		if _, exists := fixedIP[key]; exists {
			return errors.New("Trying to register " + ip.String() + " which already registered")
		}
	}
	for _, ip := range ips {
		key := ip.String()
		fixedIP[key] = FakeContainerId
	}
	return nil
}

func UnRegisterFixedIP(ips []net.IP) error {
	if ips == nil || len(ips) == 0 {
		return nil
	}
	fixed_ip_lock.Lock()
	defer fixed_ip_lock.Unlock()
	//Ensure request ips are not allocated yet.
	for _, ip := range ips {
		key := ip.String()
		if cid, exists := fixedIP[key]; exists {
			if cid == FakeContainerId {
				return errors.New("Trying to unregister " + ip.String() + " which is in use now")
			}
		} else {
			return errors.New("Trying to unregister " + ip.String() + " which not exists in the fixed ip pool")
		}
	}
	for _, ip := range ips {
		key := ip.String()
		if _, exists := fixedIP[key]; exists {
			delete(fixedIP, key)
		}
	}
	return nil
}

func FixedIP() map[string]string {
	fixed_ip_lock.Lock()
	defer fixed_ip_lock.Unlock()
	copyMap := make(map[string]string)
	for k, v := range fixedIP {
		copyMap[k] = v
	}
	return copyMap
}

func RequestFixedIP(newcid string, ip net.IP) (net.IP, error) {
	fixed_ip_lock.Lock()
	defer fixed_ip_lock.Unlock()
	if ip != nil {
		key := ip.String()
		if cid, exists := fixedIP[key]; exists {
			if cid != FakeContainerId {
				return nil, ErrFixedIPAlreadyAllocated
			}
			fixedIP[key] = newcid
			return ip, nil
		} else {
			return nil, ErrIPOutOfRange
		}
	} else {
		for k, cid := range fixedIP {
			if cid == FakeContainerId {
				fixedIP[k] = newcid
				return net.ParseIP(k), nil
			}
		}
		return nil, ErrNoAvailableIPs
	}
}

// ReleaseIP adds the provided ip back into the pool of
// available ips to be returned for use.
func ReleaseFixedIP(ip net.IP) error {
	fixed_ip_lock.Lock()
	defer fixed_ip_lock.Unlock()
	key := ip.String()
	if cid, exists := fixedIP[key]; exists {
		if cid != FakeContainerId {
			fixedIP[key] = FakeContainerId
		} else {
			logrus.Infof("tring to release an unallocated fixed ip %s", ip.String())
		}
	} else {
		logrus.Infof("tring to release an unregistered fixed ip %s", ip.String())
	}
	return nil
}
