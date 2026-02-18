package mcs

import (
	"fmt"
	"net"
	"sync"

	"k8s.io/klog/v2"
)

// ClusterSetIPAllocator manages ClusterSet-scoped IP allocation.
// IPs are allocated from a dedicated CIDR range and are consistent across all clusters.
type ClusterSetIPAllocator struct {
	cidr      *net.IPNet
	allocated map[string]string // key: namespace/name, value: allocated IP
	used      map[string]bool   // track which IPs are in use
	mu        sync.Mutex
}

// NewClusterSetIPAllocator creates a new ClusterSetIP allocator.
func NewClusterSetIPAllocator(cidr string) (*ClusterSetIPAllocator, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %s: %w", cidr, err)
	}

	return &ClusterSetIPAllocator{
		cidr:      ipnet,
		allocated: make(map[string]string),
		used:      make(map[string]bool),
	}, nil
}

// AllocateIP allocates a ClusterSetIP for a service.
// If the service already has an IP allocated, it returns the existing IP.
func (a *ClusterSetIPAllocator) AllocateIP(namespace, name string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	key := fmt.Sprintf("%s/%s", namespace, name)

	// Check if already allocated
	if ip, exists := a.allocated[key]; exists {
		klog.V(4).Infof("Service %s already has ClusterSetIP: %s", key, ip)
		return ip, nil
	}

	// Find next available IP
	ip := a.nextAvailableIP()
	if ip == "" {
		return "", fmt.Errorf("no available IPs in ClusterSet CIDR %s", a.cidr.String())
	}

	a.allocated[key] = ip
	a.used[ip] = true
	klog.Infof("Allocated ClusterSetIP %s for service %s", ip, key)

	return ip, nil
}

// ReleaseIP releases a ClusterSetIP when a service is deleted.
func (a *ClusterSetIPAllocator) ReleaseIP(namespace, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	key := fmt.Sprintf("%s/%s", namespace, name)

	if ip, exists := a.allocated[key]; exists {
		delete(a.allocated, key)
		delete(a.used, ip)
		klog.Infof("Released ClusterSetIP %s for service %s", ip, key)
	}
}

// GetIP returns the allocated IP for a service, if any.
func (a *ClusterSetIPAllocator) GetIP(namespace, name string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	key := fmt.Sprintf("%s/%s", namespace, name)
	ip, exists := a.allocated[key]
	return ip, exists
}

// nextAvailableIP finds the next available IP in the CIDR range.
func (a *ClusterSetIPAllocator) nextAvailableIP() string {
	// Start from the first usable IP (network address + 1)
	ip := make(net.IP, len(a.cidr.IP))
	copy(ip, a.cidr.IP)

	// Skip network address
	incrementIP(ip)

	// Try to find an unused IP
	for a.cidr.Contains(ip) {
		ipStr := ip.String()
		if !a.used[ipStr] {
			return ipStr
		}
		incrementIP(ip)
	}

	return ""
}

// incrementIP increments an IP address by 1.
func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}
