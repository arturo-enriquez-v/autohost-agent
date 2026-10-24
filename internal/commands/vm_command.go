// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"
)

var safeVMName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

var knownBaseImages = map[string]string{
	"ubuntu-24.04": "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
	"ubuntu-22.04": "https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img",
	"ubuntu":       "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
	"debian-12":    "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2",
	"debian":       "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2",
	"alpine":       "https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/cloud/nocloud_alpine-3.20.0-x86_64-bios-cloudinit-r0.qcow2",
}

const domainXMLTemplate = `<domain type='kvm'>
  <name>{{ .Name }}</name>
  <memory unit='MiB'>{{ .RAMMB }}</memory>
  <currentMemory unit='MiB'>{{ .RAMMB }}</currentMemory>
  <vcpu placement='static'>{{ .VCPUs }}</vcpu>
  <os>
    <type arch='x86_64'>hvm</type>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/>
    <apic/>
  </features>
  <cpu mode='host-passthrough' check='none'/>
  <clock offset='utc'/>
  <on_poweroff>destroy</on_poweroff>
  <on_reboot>restart</on_reboot>
  <on_crash>destroy</on_crash>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='{{ .DiskPath }}'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='{{ .ISOPath }}'/>
      <target dev='sda' bus='sata'/>
      <readonly/>
    </disk>
    <interface type='network'>
      <source network='default'/>
      <model type='virtio'/>
    </interface>
    <serial type='pty'>
      <target type='isa-serial' port='0'>
        <model name='isa-serial'/>
      </target>
    </serial>
    <console type='pty'>
      <target type='serial' port='0'/>
    </console>
    <channel type='unix'>
      <target type='virtio' name='org.qemu.guest_agent.0'/>
    </channel>
    <graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'>
      <listen type='address' address='127.0.0.1'/>
    </graphics>
    <video>
      <model type='virtio' heads='1' primary='yes'/>
    </video>
    <memballoon model='virtio'/>
    <rng model='virtio'>
      <backend model='random'>/dev/urandom</backend>
    </rng>
  </devices>
</domain>
`

type vmConfig struct {
	VMID        string
	Name        string
	VCPUs       int
	RAMMB       int
	DiskGB      int
	Image       string
	User        string
	SSHKeys     []string
	EnrollToken string
	CloudAPIURL string
	DiskPath    string
	ISOPath     string
}

// execVMCommand handles vm.* job types dispatched from the cloud API.
//
// Supported patterns:
//
//	vm.create               → provisions and starts a new KVM VM from payload
//	vm.start.<name>         → virsh start <name>
//	vm.stop.<name>          → virsh shutdown <name>
//	vm.reboot.<name>        → virsh reboot <name>
//	vm.destroy.<name>       → virsh destroy <name> && virsh undefine <name> --remove-all-storage
//	vm.list                 → virsh list --all
func execVMCommand(ctx context.Context, jobType string, payload map[string]any) ExecuteResult {
	if jobType == "vm.create" {
		return createVM(ctx, payload)
	}

	if jobType == "vm.list" {
		return listVMs(ctx)
	}

	parts := strings.SplitN(jobType, ".", 3)
	if len(parts) < 3 || parts[0] != "vm" {
		return ExecuteResult{Err: fmt.Errorf("invalid vm command: %q", jobType)}
	}

	action := parts[1]
	name := parts[2]
	if !safeVMName.MatchString(name) {
		return ExecuteResult{Err: fmt.Errorf("invalid VM name %q", name)}
	}

	switch action {
	case "start":
		return runVirsh(ctx, "start", name)
	case "stop":
		return runVirsh(ctx, "shutdown", name)
	case "reboot":
		return runVirsh(ctx, "reboot", name)
	case "destroy":
		return destroyVM(ctx, name)
	default:
		return ExecuteResult{Err: fmt.Errorf("unsupported vm action %q", action)}
	}
}

func runVirsh(ctx context.Context, args ...string) ExecuteResult {
	if _, err := exec.LookPath("virsh"); err != nil {
		return ExecuteResult{Err: fmt.Errorf("virsh not found: run 'autohost setup hypervisor' first")}
	}

	cmdArgs := append([]string{"-c", "qemu:///system"}, args...)
	cmd := exec.CommandContext(ctx, "virsh", cmdArgs...)
	out, err := cmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(out))
	if err != nil {
		return ExecuteResult{Output: outputStr, Err: fmt.Errorf("virsh %s failed: %w: %s", strings.Join(args, " "), err, outputStr)}
	}
	return ExecuteResult{Output: outputStr}
}

func listVMs(ctx context.Context) ExecuteResult {
	return runVirsh(ctx, "list", "--all")
}

func destroyVM(ctx context.Context, name string) ExecuteResult {
	if _, err := exec.LookPath("virsh"); err != nil {
		return ExecuteResult{Err: fmt.Errorf("virsh not found: run 'autohost setup hypervisor' first")}
	}

	// Try graceful destroy (kill running domain)
	_ = exec.CommandContext(ctx, "virsh", "-c", "qemu:///system", "destroy", name).Run()

	// Undefine domain and remove libvirt storage volumes
	undefineCmd := exec.CommandContext(ctx, "virsh", "-c", "qemu:///system", "undefine", name, "--remove-all-storage")
	out, err := undefineCmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(out))
	if err != nil {
		// Fallback without --remove-all-storage in case storage was already detached
		_ = exec.CommandContext(ctx, "virsh", "-c", "qemu:///system", "undefine", name).Run()
	}

	// Clean up any remaining disk / cidata files in the storage dir
	storageDir := getVMStorageDir()
	_ = os.Remove(filepath.Join(storageDir, name+".qcow2"))
	_ = os.Remove(filepath.Join(storageDir, name+"-cidata.iso"))

	return ExecuteResult{Output: fmt.Sprintf("VM %s destroyed successfully: %s", name, outputStr)}
}

func createVM(ctx context.Context, payload map[string]any) ExecuteResult {
	if _, err := exec.LookPath("virsh"); err != nil {
		return ExecuteResult{Err: fmt.Errorf("virsh not found: run 'autohost setup hypervisor' first")}
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		return ExecuteResult{Err: fmt.Errorf("qemu-img not found: run 'autohost setup hypervisor' first")}
	}

	cfg, err := parseVMConfig(payload)
	if err != nil {
		return ExecuteResult{Err: err}
	}

	storageDir := getVMStorageDir()
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return ExecuteResult{Err: fmt.Errorf("failed to create storage dir %s: %w", storageDir, err)}
	}

	// 1. Ensure base image is available
	baseImagePath, err := ensureBaseImage(ctx, cfg.Image, storageDir)
	if err != nil {
		return ExecuteResult{Err: fmt.Errorf("failed to get base image: %w", err)}
	}

	// 2. Create CoW instance disk
	cfg.DiskPath = filepath.Join(storageDir, cfg.Name+".qcow2")
	_ = os.Remove(cfg.DiskPath) // Clean up any stale disk

	createDiskCmd := exec.CommandContext(ctx, "qemu-img", "create", "-f", "qcow2", "-F", "qcow2", "-b", baseImagePath, cfg.DiskPath, fmt.Sprintf("%dG", cfg.DiskGB))
	if out, err := createDiskCmd.CombinedOutput(); err != nil {
		return ExecuteResult{Err: fmt.Errorf("failed to create qcow2 disk: %w: %s", err, string(out))}
	}
	_ = os.Chmod(cfg.DiskPath, 0664)

	// 3. Generate Cloud-Init ISO
	cfg.ISOPath = filepath.Join(storageDir, cfg.Name+"-cidata.iso")
	_ = os.Remove(cfg.ISOPath) // Clean up any stale ISO

	if err := generateCloudInitISO(ctx, cfg); err != nil {
		_ = os.Remove(cfg.DiskPath)
		return ExecuteResult{Err: fmt.Errorf("failed to generate cloud-init ISO: %w", err)}
	}
	_ = os.Chmod(cfg.ISOPath, 0664)

	// 4. Generate libvirt Domain XML
	xmlPath := filepath.Join(storageDir, cfg.Name+".xml")
	defer os.Remove(xmlPath)

	tmpl, err := template.New("domain").Parse(domainXMLTemplate)
	if err != nil {
		return ExecuteResult{Err: fmt.Errorf("template parse error: %w", err)}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return ExecuteResult{Err: fmt.Errorf("template execute error: %w", err)}
	}

	if err := os.WriteFile(xmlPath, buf.Bytes(), 0644); err != nil {
		return ExecuteResult{Err: fmt.Errorf("failed to write domain xml: %w", err)}
	}

	// 5. Define VM in libvirt
	defineCmd := exec.CommandContext(ctx, "virsh", "-c", "qemu:///system", "define", xmlPath)
	if out, err := defineCmd.CombinedOutput(); err != nil {
		return ExecuteResult{Err: fmt.Errorf("virsh define error: %w: %s", err, string(out))}
	}

	// 6. Start VM
	startCmd := exec.CommandContext(ctx, "virsh", "-c", "qemu:///system", "start", cfg.Name)
	if out, err := startCmd.CombinedOutput(); err != nil {
		return ExecuteResult{Err: fmt.Errorf("virsh start error: %w: %s", err, string(out))}
	}

	return ExecuteResult{Output: fmt.Sprintf("VM %s created and started successfully", cfg.Name)}
}

func parseVMConfig(payload map[string]any) (*vmConfig, error) {
	name, _ := payload["name"].(string)
	name = strings.TrimSpace(name)
	if !safeVMName.MatchString(name) {
		return nil, fmt.Errorf("invalid VM name %q: must be alphanumeric (1-64 chars)", name)
	}

	vmID, _ := payload["vm_id"].(string)
	image, _ := payload["image"].(string)
	if image == "" {
		image = "ubuntu-24.04"
	}

	user, _ := payload["user"].(string)
	if user == "" {
		user = "autohost"
	}

	vcpus := getIntFromPayload(payload, "vcpus", 1)
	ramMB := getIntFromPayload(payload, "ram_mb", 1024)
	diskGB := getIntFromPayload(payload, "disk_gb", 10)

	var sshKeys []string
	if rawKeys, ok := payload["ssh_keys"].([]any); ok {
		for _, k := range rawKeys {
			if s, ok := k.(string); ok && strings.TrimSpace(s) != "" {
				sshKeys = append(sshKeys, strings.TrimSpace(s))
			}
		}
	} else if strKeys, ok := payload["ssh_keys"].([]string); ok {
		sshKeys = strKeys
	}

	enrollToken, _ := payload["enroll_token"].(string)
	cloudAPIURL, _ := payload["cloud_api_url"].(string)

	return &vmConfig{
		VMID:        vmID,
		Name:        name,
		VCPUs:       vcpus,
		RAMMB:       ramMB,
		DiskGB:      diskGB,
		Image:       image,
		User:        user,
		SSHKeys:     sshKeys,
		EnrollToken: enrollToken,
		CloudAPIURL: cloudAPIURL,
	}, nil
}

func getIntFromPayload(payload map[string]any, key string, def int) int {
	if val, ok := payload[key]; ok {
		switch v := val.(type) {
		case float64:
			return int(v)
		case int:
			return v
		case int64:
			return int(v)
		case json.Number:
			if n, err := v.Int64(); err == nil {
				return int(n)
			}
		}
	}
	return def
}

func getVMStorageDir() string {
	// 1. Dedicated autohost state directory (created by systemd StateDirectory=autohost, writable under ProtectSystem=strict)
	autohostDir := "/var/lib/autohost/vms"
	if err := os.MkdirAll(autohostDir, 0755); err == nil && checkDirWritable(autohostDir) == nil {
		return autohostDir
	}

	// 2. Standard libvirt path if writable
	defaultDir := "/var/lib/libvirt/images"
	if err := checkDirWritable(defaultDir); err == nil {
		return defaultDir
	}

	// 3. Fallback to user home directory if writable
	if home, err := os.UserHomeDir(); err == nil {
		homeDir := filepath.Join(home, ".autohost", "vms")
		if err := os.MkdirAll(homeDir, 0755); err == nil && checkDirWritable(homeDir) == nil {
			return homeDir
		}
	}

	// 4. Temporary fallback
	tmpDir := "/tmp/autohost-vms"
	_ = os.MkdirAll(tmpDir, 0755)
	return tmpDir
}

func checkDirWritable(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	testFile := filepath.Join(dir, fmt.Sprintf(".perm_test_%d", time.Now().UnixNano()))
	if err := os.WriteFile(testFile, []byte("test"), 0600); err != nil {
		return err
	}
	_ = os.Remove(testFile)
	return nil
}

func ensureBaseImage(ctx context.Context, image string, storageDir string) (string, error) {
	baseDir := filepath.Join(storageDir, "base")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return "", err
	}

	imageURL := image
	imageName := image
	if url, ok := knownBaseImages[strings.ToLower(image)]; ok {
		imageURL = url
		imageName = strings.ToLower(image)
	} else if strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
		imageURL = image
		imageName = filepath.Base(image)
	}

	destFile := filepath.Join(baseDir, imageName+".img")
	if _, err := os.Stat(destFile); err == nil {
		return destFile, nil
	}

	// Download image with streaming to temp file
	tmpFile := destFile + ".part"
	_ = os.Remove(tmpFile)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", fmt.Errorf("invalid image URL: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download base image error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download base image failed with status %d", resp.StatusCode)
	}

	out, err := os.Create(tmpFile)
	if err != nil {
		return "", err
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = os.Remove(tmpFile)
		return "", fmt.Errorf("copy base image failed: %w", err)
	}
	_ = out.Close()

	if err := os.Rename(tmpFile, destFile); err != nil {
		return "", err
	}
	_ = os.Chmod(destFile, 0664)

	return destFile, nil
}

func generateCloudInitISO(ctx context.Context, cfg *vmConfig) error {
	tmpDir, err := os.MkdirTemp("", "cidata-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	metaDataPath := filepath.Join(tmpDir, "meta-data")
	userDataPath := filepath.Join(tmpDir, "user-data")

	metaDataContent := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", cfg.VMID, cfg.Name)
	if err := os.WriteFile(metaDataPath, []byte(metaDataContent), 0644); err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("#cloud-config\n")
	sb.WriteString("users:\n")
	sb.WriteString(fmt.Sprintf("  - name: %s\n", cfg.User))
	sb.WriteString("    groups: sudo\n")
	sb.WriteString("    shell: /bin/bash\n")
	sb.WriteString("    sudo: ['ALL=(ALL) NOPASSWD:ALL']\n")

	if len(cfg.SSHKeys) > 0 {
		sb.WriteString("    ssh_authorized_keys:\n")
		for _, key := range cfg.SSHKeys {
			sb.WriteString(fmt.Sprintf("      - %s\n", key))
		}
	}

	sb.WriteString("ssh_pwauth: false\n")
	sb.WriteString("package_update: true\n")
	sb.WriteString("packages:\n")
	sb.WriteString("  - curl\n")
	sb.WriteString("  - ca-certificates\n")

	if cfg.EnrollToken != "" {
		apiURL := getFallbackAPIURL(cfg.CloudAPIURL)
		if apiURL != "" {
			sb.WriteString("runcmd:\n")
			sb.WriteString("  - [ sh, -c, \"curl -fsSL https://raw.githubusercontent.com/mazapanuwu13/autohost-cli/main/scripts/install.sh | bash || true\" ]\n")
			sb.WriteString("  - [ sh, -c, \"curl -fsSL https://raw.githubusercontent.com/mazapanuwu13/autohost-agent/main/scripts/install.sh | bash || true\" ]\n")
			sb.WriteString(fmt.Sprintf("  - [ autohost, enroll, link, --api, %s, --token, %s ]\n", apiURL, cfg.EnrollToken))
			sb.WriteString("  - [ systemctl, restart, autohost-agent ]\n")
		}
	}

	if err := os.WriteFile(userDataPath, []byte(sb.String()), 0644); err != nil {
		return err
	}

	// 1. Check cloud-localds
	if _, err := exec.LookPath("cloud-localds"); err == nil {
		cmd := exec.CommandContext(ctx, "cloud-localds", cfg.ISOPath, userDataPath, metaDataPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("cloud-localds failed: %w: %s", err, string(out))
		}
		return nil
	}

	// 2. Check genisoimage
	if _, err := exec.LookPath("genisoimage"); err == nil {
		cmd := exec.CommandContext(ctx, "genisoimage", "-output", cfg.ISOPath, "-volid", "cidata", "-joliet", "-rock", userDataPath, metaDataPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("genisoimage failed: %w: %s", err, string(out))
		}
		return nil
	}

	// 3. Check mkisofs
	if _, err := exec.LookPath("mkisofs"); err == nil {
		cmd := exec.CommandContext(ctx, "mkisofs", "-output", cfg.ISOPath, "-volid", "cidata", "-joliet", "-rock", userDataPath, metaDataPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("mkisofs failed: %w: %s", err, string(out))
		}
		return nil
	}

	return fmt.Errorf("neither cloud-localds, genisoimage nor mkisofs found: run 'autohost setup hypervisor'")
}

func getFallbackAPIURL(given string) string {
	if given != "" && !strings.Contains(given, "localhost") && !strings.Contains(given, "127.0.0.1") {
		return given
	}
	// Try reading api_url from /etc/autohost/config.yaml
	if data, err := os.ReadFile("/etc/autohost/config.yaml"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "api_url:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "api_url:"))
				val = strings.Trim(val, `"'`)
				if val != "" && !strings.Contains(val, "localhost") && !strings.Contains(val, "127.0.0.1") {
					return val
				}
			}
		}
	}
	return given
}
