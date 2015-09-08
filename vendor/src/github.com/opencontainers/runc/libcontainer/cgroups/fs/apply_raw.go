// +build linux

package fs

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"sync"

	"github.com/opencontainers/runc/libcontainer/cgroups"
	"github.com/opencontainers/runc/libcontainer/configs"
	log "github.com/Sirupsen/logrus"
)

var (
	subsystems = map[string]subsystem{
		"devices":    &DevicesGroup{},
		"memory":     &MemoryGroup{},
		"cpu":        &CpuGroup{},
		"cpuset":     &CpusetGroup{},
		"cpuacct":    &CpuacctGroup{},
		"blkio":      &BlkioGroup{},
		"hugetlb":    &HugetlbGroup{},
		"net_cls":    &NetClsGroup{},
		"net_prio":   &NetPrioGroup{},
		"perf_event": &PerfEventGroup{},
		"freezer":    &FreezerGroup{},
	}
	CgroupProcesses  = "cgroup.procs"
	HugePageSizes, _ = cgroups.GetHugePageSize()
)

type subsystem interface {
	// Returns the stats, as 'stats', corresponding to the cgroup under 'path'.
	GetStats(path string, stats *cgroups.Stats) error
	// Removes the cgroup represented by 'data'.
	Remove(*data) error
	// Creates and joins the cgroup represented by data.
	Apply(*data) error
	// Set the cgroup represented by cgroup.
	Set(path string, cgroup *configs.Cgroup) error
}

type Manager struct {
	mu      sync.Mutex
	Cgroups *configs.Cgroup
	Paths   map[string]string
}

// The absolute path to the root of the cgroup hierarchies.
var cgroupRootLock sync.Mutex
var cgroupRoot string
var dockerGid int = 0
var filePerm = os.FileMode(0664)
var dirPerm = os.FileMode(0775)
var once sync.Once

func getDockerGid() {
	once.Do(func() {
		info, err := os.Stat("/var/run/docker.sock")
		log.Infof("get docker gid")
		if info != nil {
			dockerGid = int(info.Sys().(*syscall.Stat_t).Gid)
			log.Infof("docker gid %d", dockerGid)
		} else {
			log.Errorf("failed to get docker gid, %v", err)
		}
	})
}

// Gets the cgroupRoot.
func getCgroupRoot() (string, error) {
	cgroupRootLock.Lock()
	defer cgroupRootLock.Unlock()

	if cgroupRoot != "" {
		return cgroupRoot, nil
	}

	root, err := cgroups.FindCgroupMountpointDir()
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(root); err != nil {
		return "", err
	}

	cgroupRoot = root
	return cgroupRoot, nil
}

type data struct {
	root   string
	dockerRoot string  // /${root}/${cgroup} == /${root}/cpu/${dockerRoot}/${ID}
	cgroup string
	c      *configs.Cgroup
	pid    int
}

func (m *Manager) Apply(pid int) error {
	if m.Cgroups == nil {
		return nil
	}

	var c = m.Cgroups

	d, err := getCgroupData(m.Cgroups, pid)
	if err != nil {
		return err
	}

	paths := make(map[string]string)
	defer func() {
		if err != nil {
			cgroups.RemovePaths(paths)
		}
	}()
	for name, sys := range subsystems {
		if err := sys.Apply(d); err != nil {
			return err
		}
		// TODO: Apply should, ideally, be reentrant or be broken up into a separate
		// create and join phase so that the cgroup hierarchy for a container can be
		// created then join consists of writing the process pids to cgroup.procs
		p, err := d.path(name)
		if err != nil {
			if cgroups.IsNotFound(err) {
				continue
			}
			return err
		}
		paths[name] = p
	}
	m.Paths = paths

	if paths["cpu"] != "" {
		if err := CheckCpushares(paths["cpu"], c.CpuShares); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) Destroy() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := cgroups.RemovePaths(m.Paths); err != nil {
		return err
	}
	m.Paths = make(map[string]string)
	return nil
}

func (m *Manager) GetPaths() map[string]string {
	m.mu.Lock()
	paths := m.Paths
	m.mu.Unlock()
	return paths
}

func (m *Manager) GetStats() (*cgroups.Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := cgroups.NewStats()
	for name, path := range m.Paths {
		sys, ok := subsystems[name]
		if !ok || !cgroups.PathExists(path) {
			continue
		}
		if err := sys.GetStats(path, stats); err != nil {
			return nil, err
		}
	}

	return stats, nil
}

func (m *Manager) Set(container *configs.Config) error {
	for name, path := range m.Paths {
		sys, ok := subsystems[name]
		if !ok || !cgroups.PathExists(path) {
			continue
		}
		if err := sys.Set(path, container.Cgroups); err != nil {
			return err
		}
	}

	return nil
}

// Freeze toggles the container's freezer cgroup depending on the state
// provided
func (m *Manager) Freeze(state configs.FreezerState) error {
	d, err := getCgroupData(m.Cgroups, 0)
	if err != nil {
		return err
	}

	dir, err := d.path("freezer")
	if err != nil {
		return err
	}

	prevState := m.Cgroups.Freezer
	m.Cgroups.Freezer = state

	freezer := subsystems["freezer"]
	err = freezer.Set(dir, m.Cgroups)
	if err != nil {
		m.Cgroups.Freezer = prevState
		return err
	}

	return nil
}

func (m *Manager) GetPids() ([]int, error) {
	d, err := getCgroupData(m.Cgroups, 0)
	if err != nil {
		return nil, err
	}

	dir, err := d.path("devices")
	if err != nil {
		return nil, err
	}

	return cgroups.ReadProcsFile(dir)
}

func getCgroupData(c *configs.Cgroup, pid int) (*data, error) {
	root, err := getCgroupRoot()
	if err != nil {
		return nil, err
	}

	cgroup := c.Name
	if c.Parent != "" {
		cgroup = filepath.Join(c.Parent, cgroup)
	}

	return &data{
		root:   root,
		dockerRoot: c.Parent,
		cgroup: cgroup,
		c:      c,
		pid:    pid,
	}, nil
}

func (raw *data) parent(subsystem, mountpoint, src string) (string, error) {
	initPath, err := cgroups.GetInitCgroupDir(subsystem)
	if err != nil {
		return "", err
	}
	relDir, err := filepath.Rel(src, initPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(mountpoint, relDir), nil
}

func (raw *data) path(subsystem string) (string, error) {
	mnt, src, err := cgroups.FindCgroupMountpointAndSource(subsystem)
	// If we didn't mount the subsystem, there is no point we make the path.
	if err != nil {
		return "", err
	}

	// If the cgroup name/path is absolute do not look relative to the cgroup of the init process.
	if filepath.IsAbs(raw.cgroup) {
		return filepath.Join(raw.root, subsystem, raw.cgroup), nil
	}

	parent, err := raw.parent(subsystem, mnt, src)
	if err != nil {
		return "", err
	}

	return filepath.Join(parent, raw.cgroup), nil
}

func (raw *data) join(subsystem string) (string, error) {
	getDockerGid()
	oldMask := syscall.Umask(0)
	defer syscall.Umask(oldMask)
	path, err := raw.path(subsystem)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(path, dirPerm); err != nil && !os.IsExist(err) {
		log.Errorf("err %v", err)
		return "", err
	}
	subsystemRoot := filepath.Join(raw.root, subsystem, raw.dockerRoot)
	if err := ensureSubsystemRoot(subsystemRoot); err != nil {
		log.Errorf("err %v", err)
		return "", err
	}
	if err := os.Chown(path, 0, dockerGid); err != nil {
		log.Errorf("err %v", err)
		return "", err
	}
	if err := ensureFiles(path); err != nil {
		log.Errorf("err %v", err)
 		return "", err
 	}
	if err := writeFile(path, CgroupProcesses, strconv.Itoa(raw.pid)); err != nil {
		log.Errorf("err %v", err)
		return "", err
	}
	return path, nil
}

func writeFile(dir, file, data string) error {
	// Normally dir should not be empty, one case is that cgroup subsystem
	// is not mounted, we will get empty dir, and we want it fail here.
	if dir == "" {
		return fmt.Errorf("no such directory for %s.", file)
	}
	getDockerGid()
	oldMask := syscall.Umask(0)
	defer syscall.Umask(oldMask)
	fileName := filepath.Join(dir, file)
	if err := ioutil.WriteFile(fileName, []byte(data), filePerm); err != nil {
		log.Errorf("err %v", err)
		return err
	}
	if err := os.Chown(fileName, 0, dockerGid); err != nil {
		log.Errorf("err %v", err)
		return err
	}
	return nil
}

func readFile(dir, file string) (string, error) {
	data, err := ioutil.ReadFile(filepath.Join(dir, file))
	return string(data), err
}

//ensure /cgroupdir/${subsystem}/docker is owner by docker group
func ensureSubsystemRoot(subsystemRoot string) error {
	info, _ := os.Stat(subsystemRoot)
	expected := true
	if int(info.Sys().(*syscall.Stat_t).Gid) != dockerGid {
		if err := os.Chown(subsystemRoot, 0, dockerGid); err != nil {
			log.Errorf("chown file %s err %v", subsystemRoot, err)
			return err
		}
		expected = false
	}
	if uint32(info.Mode()) != uint32(dirPerm) {
		if err := os.Chmod(subsystemRoot, dirPerm); err != nil {
			log.Errorf("chmod file %s err %v", subsystemRoot, err)
			return err
		}
		expected = false
	}
	if !expected {
		if err := ensureFiles(subsystemRoot); err != nil {
			return err
		}
	}
	return nil
}

//ensure files under ${dir} is owner by docker group and the permission is expected
func ensureFiles(dir string) error {
	files, _:= ioutil.ReadDir(dir)
	var perm os.FileMode = filePerm
	for _, f := range files {
		if f.IsDir() {
			perm = dirPerm
		} else {
			perm = filePerm
		}
		fileName := filepath.Join(dir, f.Name())
		if err := os.Chown(fileName, 0, dockerGid); err != nil {
			log.Errorf("chown file %s err %v", fileName, err)
			return err
		}
		if err := os.Chmod(fileName, perm); err != nil {
			log.Errorf("chmod file %s err %v", fileName, err)
			return err
		}
	}
	return nil
}

func removePath(p string, err error) error {
	if err != nil {
		return err
	}
	if p != "" {
		return os.RemoveAll(p)
	}
	return nil
}

func CheckCpushares(path string, c int64) error {
	var cpuShares int64

	if c == 0 {
		return nil
	}

	fd, err := os.Open(filepath.Join(path, "cpu.shares"))
	if err != nil {
		return err
	}
	defer fd.Close()

	_, err = fmt.Fscanf(fd, "%d", &cpuShares)
	if err != nil && err != io.EOF {
		return err
	}

	if c > cpuShares {
		return fmt.Errorf("The maximum allowed cpu-shares is %d", cpuShares)
	} else if c < cpuShares {
		return fmt.Errorf("The minimum allowed cpu-shares is %d", cpuShares)
	}

	return nil
}
