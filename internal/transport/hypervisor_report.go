package transport

import (
	"context"
	"log"

	pb "autohost-agent/internal/grpc/nodepb"
	"autohost-agent/internal/hypervisor"
)

// sendHypervisorInfo probes the host once per Connect session and reports the
// result when KVM + libvirt are usable. Hosts that are not hypervisors stay
// silent so regular nodes produce no extra traffic or log noise.
func (c *GRPCClient) sendHypervisorInfo(ctx context.Context, out chan<- *pb.NodeMessage) {
	info := hypervisor.NewDetector().Detect(ctx)
	if !info.Capable {
		return
	}

	msg := &pb.NodeMessage{
		Payload: &pb.NodeMessage_HypervisorInfo{
			HypervisorInfo: &pb.HypervisorInfoPayload{
				Capable:               info.Capable,
				KvmAvailable:          info.KVMAvailable,
				LibvirtAvailable:      info.LibvirtAvailable,
				LibvirtVersion:        info.LibvirtVersion,
				Reason:                info.Reason,
				NetworkName:           info.NetworkName,
				CpuCores:              info.CPUCores,
				RamTotalBytes:         info.RAMTotalBytes,
				StorageTotalBytes:     info.StorageTotalBytes,
				StorageAvailableBytes: info.StorageAvailableBytes,
			},
		},
	}

	select {
	case out <- msg:
		log.Printf("🖥️  hypervisor: reported capacity (libvirt %s, %d cores)", info.LibvirtVersion, info.CPUCores)
	case <-ctx.Done():
	}
}
