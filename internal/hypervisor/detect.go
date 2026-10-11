// Package hypervisor detects whether the host can run virtual machines
// (KVM + libvirt) and reports the resources available for them.
//
// libvirt is driven through the `virsh` CLI rather than libvirt-go so the agent
// stays a static, CGO-free binary that can be cross-compiled.
package hypervisor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// connectURI is the system-wide libvirt daemon (VMs survive agent restarts).
	connectURI = "qemu:///system"
	// DefaultNetwork is the NAT network libvirt ships with (virbr0).
	DefaultNetwork = "default"
	// DefaultPoolPath is where qcow2 volumes live.
	DefaultPoolPath = "/var/lib/libvirt/images"

	kvmDevice     = "/dev/kvm"
	virshTimeout  = 10 * time.Second
	meminfoPath   = "/proc/meminfo"
	virshBinary   = "virsh"
	reasonNoKVM   = "/dev/kvm no disponible: la CPU no soporta virtualización o está desactivada en la BIOS"
	reasonNoVirsh = "virsh no está instalado: ejecuta `autohost setup hypervisor`"
)

// Info describes the virtualization capabilities of the host.
type Info struct {
	Capable               bool
	KVMAvailable          bool
	LibvirtAvailable      bool
	LibvirtVersion        string
	Reason                string
	NetworkName           string
	CPUCores              uint32
	RAMTotalBytes         uint64
	StorageTotalBytes     uint64
	StorageAvailableBytes uint64
}

// Detector probes the host. Every OS interaction is injectable so the logic
// can be unit-tested without KVM.
type Detector struct {
	// FileExists reports whether a path exists.
	FileExists func(path string) bool
	// Virsh runs `virsh -c qemu:///system <args...>` and returns stdout.
	Virsh func(ctx context.Context, args ...string) (string, error)
	// LookPath resolves an executable (exec.LookPath semantics).
	LookPath func(file string) (string, error)
	// ReadMemTotal returns the host RAM in bytes.
	ReadMemTotal func() (uint64, error)
	// Statfs returns total and available bytes for the filesystem holding path.
	Statfs func(path string) (total, avail uint64, err error)
	// NumCPU returns the number of logical cores.
	NumCPU func() int

	PoolPath string
}

// NewDetector returns a Detector wired to the real host.
func NewDetector() *Detector {
	return &Detector{
		FileExists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		Virsh: func(ctx context.Context, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, virshTimeout)
			defer cancel()
			full := append([]string{"-c", connectURI}, args...)
			out, err := exec.CommandContext(ctx, virshBinary, full...).Output()
			return string(out), err
		},
		LookPath:     exec.LookPath,
		ReadMemTotal: readMemTotal,
		Statfs:       statfs,
		NumCPU:       runtime.NumCPU,
		PoolPath:     DefaultPoolPath,
	}
}

// Detect inspects the host and never returns an error: failures are encoded in
// Info.Reason so callers can always report something meaningful.
func (d *Detector) Detect(ctx context.Context) Info {
	info := Info{
		NetworkName: DefaultNetwork,
		CPUCores:    uint32(d.NumCPU()),
	}

	if ram, err := d.ReadMemTotal(); err == nil {
		info.RAMTotalBytes = ram
	}
	if total, avail, err := d.Statfs(d.statPath()); err == nil {
		info.StorageTotalBytes, info.StorageAvailableBytes = total, avail
	}

	info.KVMAvailable = d.FileExists(kvmDevice)
	if !info.KVMAvailable {
		info.Reason = reasonNoKVM
		return info
	}

	if _, err := d.LookPath(virshBinary); err != nil {
		info.Reason = reasonNoVirsh
		return info
	}

	out, err := d.Virsh(ctx, "version", "--daemon")
	if err != nil {
		info.Reason = fmt.Sprintf("libvirtd no responde en %s: %v", connectURI, err)
		return info
	}
	info.LibvirtAvailable = true
	info.LibvirtVersion = parseLibvirtVersion(out)
	info.Capable = true
	return info
}

// statPath is the directory used to size the storage pool. When the libvirt
// pool dir does not exist yet it falls back to its closest existing parent.
func (d *Detector) statPath() string {
	p := d.PoolPath
	for p != "/" && p != "" && !d.FileExists(p) {
		idx := strings.LastIndex(p, "/")
		if idx <= 0 {
			return "/"
		}
		p = p[:idx]
	}
	if p == "" {
		return "/"
	}
	return p
}

// parseLibvirtVersion extracts the "Running against daemon: X.Y.Z" value from
// `virsh version --daemon`, falling back to the "Using library" line.
func parseLibvirtVersion(out string) string {
	var fallback string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "Running against daemon:"):
			return strings.TrimSpace(strings.TrimPrefix(line, "Running against daemon:"))
		case strings.HasPrefix(line, "Using library: libvirt"):
			fallback = strings.TrimSpace(strings.TrimPrefix(line, "Using library: libvirt"))
		}
	}
	return fallback
}

func readMemTotal() (uint64, error) {
	f, err := os.Open(meminfoPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, err
			}
			return kb * 1024, nil
		}
	}
	return 0, fmt.Errorf("MemTotal not found in %s", meminfoPath)
}

func statfs(path string) (uint64, uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize)
	return st.Blocks * bsize, st.Bavail * bsize, nil
}
