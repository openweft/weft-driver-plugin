package weftdriverplugin

// transport_test.go exercises the full server↔client datapath in-process over
// an in-memory bufconn — every conversion plus the sentinel/ctx error mapping —
// without spawning a plugin subprocess. The go-plugin handshake itself is
// smoked separately against the real (pure-Go) weft-driver-qemu binary.

import (
	"context"
	"errors"
	"net"
	"testing"

	drivers "github.com/openweft/weft-drivers"
	"github.com/openweft/weft-driver-plugin/driverpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// fakeDriver implements all four driver interfaces. It records the last spec it
// saw and can be told to fail with a chosen error.
type fakeDriver struct {
	hostInfo drivers.HostInfo
	failWith error

	lastVMSpec   drivers.VMSpec
	lastDisk     drivers.DiskSpec
	lastNIC      drivers.NICHandle
	lastNetSpec  drivers.NetworkSpec
	lastPortSpec drivers.PortSpec
	lastVolSpec  drivers.VolumeSpec
	attachReturn drivers.NICHandle
}

func (f *fakeDriver) HostInfo(context.Context) (drivers.HostInfo, error) {
	return f.hostInfo, f.failWith
}

// HypervisorDriver
func (f *fakeDriver) CreateVM(_ context.Context, s drivers.VMSpec) error { f.lastVMSpec = s; return f.failWith }
func (f *fakeDriver) StartVM(context.Context, string) error              { return f.failWith }
func (f *fakeDriver) StopVM(context.Context, string) error               { return f.failWith }
func (f *fakeDriver) DeleteVM(context.Context, string) error             { return f.failWith }
func (f *fakeDriver) AttachDisk(_ context.Context, _ string, d drivers.DiskSpec) error {
	f.lastDisk = d
	return f.failWith
}
func (f *fakeDriver) DetachDisk(context.Context, string, string) error { return f.failWith }
func (f *fakeDriver) AttachNIC(_ context.Context, _ string, n drivers.NICHandle) error {
	f.lastNIC = n
	return f.failWith
}
func (f *fakeDriver) DetachNIC(context.Context, string, string) error { return f.failWith }

// NetworkDriver
func (f *fakeDriver) EnsureNetwork(_ context.Context, s drivers.NetworkSpec) error {
	f.lastNetSpec = s
	return f.failWith
}
func (f *fakeDriver) DestroyNetwork(context.Context, string) error { return f.failWith }
func (f *fakeDriver) AttachPort(_ context.Context, s drivers.PortSpec) (drivers.NICHandle, error) {
	f.lastPortSpec = s
	return f.attachReturn, f.failWith
}
func (f *fakeDriver) DetachPort(context.Context, string) error              { return f.failWith }
func (f *fakeDriver) RotateMeshPeer(context.Context, drivers.PortSpec) error { return f.failWith }

// VolumeDriver
func (f *fakeDriver) Name() string { return "fake" }
func (f *fakeDriver) Local() bool  { return true }
func (f *fakeDriver) EnsureVolume(_ context.Context, s drivers.VolumeSpec) error {
	f.lastVolSpec = s
	return f.failWith
}
func (f *fakeDriver) DestroyVolume(context.Context, string) error { return f.failWith }
func (f *fakeDriver) AttachVolume(context.Context, string, string) (drivers.AttachedVolume, error) {
	return drivers.AttachedVolume{BackingPath: "/x/disk.img", ReadOnly: true}, f.failWith
}
func (f *fakeDriver) DetachVolume(context.Context, string, string) error { return f.failWith }

// ImageDriver
func (f *fakeDriver) Pull(context.Context, string) error             { return f.failWith }
func (f *fakeDriver) LocalPath(context.Context, string) (string, error) { return "/cache/x", f.failWith }
func (f *fakeDriver) Delete(context.Context, string) error           { return f.failWith }
func (f *fakeDriver) InCache(context.Context, string) (bool, error)  { return true, f.failWith }

// dialSet wires the four server adapters around fake onto a bufconn gRPC
// server and returns a DriverSet of client stubs talking to it.
func dialSet(t *testing.T, fake *fakeDriver) *DriverSet {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	driverpb.RegisterHypervisorServer(s, &hypervisorServer{impl: fake})
	driverpb.RegisterNetworkServer(s, &networkServer{impl: fake})
	driverpb.RegisterVolumeServer(s, &volumeServer{impl: fake})
	driverpb.RegisterImageServer(s, &imageServer{impl: fake})
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return &DriverSet{
		Hypervisor: &hypervisorClient{c: driverpb.NewHypervisorClient(conn)},
		Network:    &networkClient{c: driverpb.NewNetworkClient(conn)},
		Volume:     &volumeClient{c: driverpb.NewVolumeClient(conn)},
		Image:      &imageClient{c: driverpb.NewImageClient(conn)},
	}
}

func TestRoundTrip_SpecsSurviveTheWire(t *testing.T) {
	fake := &fakeDriver{
		hostInfo:     drivers.HostInfo{UUID: "h1", Hostname: "host-1", AZ: "az-a", Hypervisor: "fake-hv", Architecture: "arm64"},
		attachReturn: drivers.NICHandle{Device: "tap9", MAC: "02:00:00:aa:bb:cc"},
	}
	set := dialSet(t, fake)
	ctx := context.Background()

	hi, err := set.Hypervisor.HostInfo(ctx)
	if err != nil || hi != fake.hostInfo {
		t.Fatalf("HostInfo round-trip: %+v err=%v", hi, err)
	}

	vm := drivers.VMSpec{UUID: "vm1", ProjectUUID: "p", Name: "n", CPUCount: 4, MemoryMiB: 2048, BootKind: "uki", BootRef: "ref", Cmdline: "console=ttyS0"}
	if err := set.Hypervisor.CreateVM(ctx, vm); err != nil {
		t.Fatal(err)
	}
	if fake.lastVMSpec != vm {
		t.Errorf("VMSpec mismatch:\n got %+v\nwant %+v", fake.lastVMSpec, vm)
	}

	disk := drivers.DiskSpec{VolumeUUID: "v", BackingPath: "/d.img", Bus: "virtio", SizeGiB: 10, ReadOnly: true, Boot: true}
	if err := set.Hypervisor.AttachDisk(ctx, "vm1", disk); err != nil {
		t.Fatal(err)
	}
	if fake.lastDisk != disk {
		t.Errorf("DiskSpec mismatch:\n got %+v\nwant %+v", fake.lastDisk, disk)
	}

	port := drivers.PortSpec{UUID: "po", VMUUID: "vm1", NetworkUUID: "net", MAC: "02:00:00:00:00:01", IP: "10.0.0.5", WireguardPubKey: "key=", MeshEndpoint: "1.2.3.4:51820", EffectiveSecurityGroups: []string{"sg1", "sg2"}}
	nic, err := set.Network.AttachPort(ctx, port)
	if err != nil {
		t.Fatal(err)
	}
	if nic != fake.attachReturn {
		t.Errorf("AttachPort handle mismatch: got %+v want %+v", nic, fake.attachReturn)
	}
	if fake.lastPortSpec.IP != "10.0.0.5" || len(fake.lastPortSpec.EffectiveSecurityGroups) != 2 {
		t.Errorf("PortSpec not carried: %+v", fake.lastPortSpec)
	}

	av, err := set.Volume.AttachVolume(ctx, "v", "h1")
	if err != nil || av.BackingPath != "/x/disk.img" || !av.ReadOnly {
		t.Errorf("AttachVolume round-trip: %+v err=%v", av, err)
	}
	if set.Volume.Name() != "fake" || !set.Volume.Local() {
		t.Errorf("Volume Name/Local not carried")
	}

	if ok, err := set.Image.InCache(ctx, "ref"); err != nil || !ok {
		t.Errorf("InCache round-trip: %v err=%v", ok, err)
	}
}

func TestErrorMapping_SentinelsSurvive(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"not-applicable", drivers.ErrNotApplicable},
		{"unsupported", drivers.ErrUnsupported},
		{"not-found", drivers.ErrNotFound},
		{"canceled", context.Canceled},
		{"deadline", context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeDriver{failWith: tc.err}
			set := dialSet(t, fake)
			// RotateMeshPeer returns ErrNotApplicable in the real mesh driver;
			// here it just relays failWith — any method exercises the mapping.
			got := set.Network.RotateMeshPeer(context.Background(), drivers.PortSpec{})
			if !errors.Is(got, tc.err) {
				t.Errorf("sentinel lost across wire: got %v want errors.Is(_, %v)", got, tc.err)
			}
		})
	}
}

func TestErrorMapping_PlainErrorKeepsMessage(t *testing.T) {
	fake := &fakeDriver{failWith: errors.New("disk is on fire")}
	set := dialSet(t, fake)
	err := set.Hypervisor.StartVM(context.Background(), "vm1")
	if err == nil || err.Error() == "" {
		t.Fatalf("expected non-empty error, got %v", err)
	}
	if got := err.Error(); got != "disk is on fire" && !contains(got, "disk is on fire") {
		t.Errorf("plain error message lost: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
