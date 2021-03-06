package fs

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"sync"

	"github.com/docker/libcontainer/cgroups"
	"github.com/docker/libcontainer/configs"
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
		"perf_event": &PerfEventGroup{},
		"freezer":    &FreezerGroup{},
		"net_cls":    &NetClsGroup{},
	}
	CgroupProcesses = "cgroup.procs"
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

	return nil
}

func (m *Manager) Destroy() error {
	return cgroups.RemovePaths(m.Paths)
}

func (m *Manager) GetPaths() map[string]string {
	return m.Paths
}

// Symmetrical public function to update device based cgroups.  Also available
// in the systemd implementation.
func ApplyDevices(c *configs.Cgroup, pid int) error {
	d, err := getCgroupData(c, pid)
	if err != nil {
		return err
	}

	devices := subsystems["devices"]

	return devices.Apply(d)
}

func (m *Manager) GetStats() (*cgroups.Stats, error) {
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

func (raw *data) parent(subsystem, mountpoint string) (string, error) {
	initPath, err := cgroups.GetInitCgroupDir(subsystem)
	if err != nil {
		return "", err
	}
	return filepath.Join(mountpoint, initPath), nil
}

func (raw *data) path(subsystem string) (string, error) {
	mnt, err := cgroups.FindCgroupMountpoint(subsystem)
	// If we didn't mount the subsystem, there is no point we make the path.
	if err != nil {
		return "", err
	}

	// If the cgroup name/path is absolute do not look relative to the cgroup of the init process.
	if filepath.IsAbs(raw.cgroup) {
		return filepath.Join(raw.root, subsystem, raw.cgroup), nil
	}

	parent, err := raw.parent(subsystem, mnt)
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

func removePath(p string, err error) error {
	if err != nil {
		return err
	}
	if p != "" {
		return os.RemoveAll(p)
	}
	return nil
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
