package weftdriverplugin

// plugin_test.go covers the go-plugin adapter glue: pluginMap, GRPCServer,
// GRPCClient. These don't need a real plugin subprocess — we drive them with
// a vanilla *grpc.Server / *grpc.ClientConn just like transport_test.go does,
// but going through the BundlePlugin methods so the registration paths get
// statement coverage too.

import (
	"context"
	"net"
	"testing"

	drivers "github.com/openweft/weft-drivers"
	plugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestPluginMap_Shape(t *testing.T) {
	m := pluginMap()
	if len(m) != 1 {
		t.Errorf("pluginMap should have 1 entry, got %d", len(m))
	}
	p, ok := m[PluginName]
	if !ok {
		t.Errorf("pluginMap missing %q", PluginName)
	}
	if _, ok := p.(*BundlePlugin); !ok {
		t.Errorf("pluginMap[%q] is %T, want *BundlePlugin", PluginName, p)
	}
}

// TestBundlePlugin_GRPCServerRegistersServices wires a BundlePlugin into a
// real grpc.Server through GRPCServer, then through GRPCClient on the other
// end of a bufconn, and confirms the two halves talk: a CreateVM call should
// land on the impl. This exercises the GRPCServer + GRPCClient methods
// without spawning a child process.
func TestBundlePlugin_GRPCServerAndClient_RoundTrip(t *testing.T) {
	fake := &fakeDriver{
		hostInfo:     drivers.HostInfo{UUID: "h"},
		attachReturn: drivers.NICHandle{Device: "tap0"},
	}
	set := &DriverSet{
		Hypervisor: fake,
		Network:    fake,
		Volume:     fake,
		Image:      fake,
	}
	bp := &BundlePlugin{Impl: set}

	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	if err := bp.GRPCServer(nil, s); err != nil {
		t.Fatalf("GRPCServer: %v", err)
	}
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

	clientBP := &BundlePlugin{} // host side: no Impl
	raw, err := clientBP.GRPCClient(context.Background(), nil, conn)
	if err != nil {
		t.Fatalf("GRPCClient: %v", err)
	}
	dispensed, ok := raw.(*DriverSet)
	if !ok {
		t.Fatalf("GRPCClient returned %T, want *DriverSet", raw)
	}

	// Smoke: every stub is callable.
	if _, err := dispensed.Hypervisor.HostInfo(context.Background()); err != nil {
		t.Errorf("Hypervisor.HostInfo via BundlePlugin: %v", err)
	}
	if err := dispensed.Hypervisor.CreateVM(context.Background(), drivers.VMSpec{UUID: "vm-bp"}); err != nil {
		t.Errorf("CreateVM via BundlePlugin: %v", err)
	}
	if fake.lastVMSpec.UUID != "vm-bp" {
		t.Errorf("BundlePlugin did not route CreateVM to impl: lastVMSpec=%+v", fake.lastVMSpec)
	}
	if _, err := dispensed.Network.AttachPort(context.Background(), drivers.PortSpec{UUID: "p"}); err != nil {
		t.Errorf("Network.AttachPort via BundlePlugin: %v", err)
	}
	if dispensed.Volume.Name() != "fake" {
		t.Errorf("Volume.Name did not pass through")
	}
	if _, err := dispensed.Image.InCache(context.Background(), "ref"); err != nil {
		t.Errorf("Image.InCache via BundlePlugin: %v", err)
	}
}

// TestBundlePlugin_PluginInterface confirms BundlePlugin satisfies the
// go-plugin Plugin interface at compile time. NetRPCUnsupportedPlugin
// provides the net/rpc no-op methods; we only implement the gRPC pair.
func TestBundlePlugin_PluginInterface(t *testing.T) {
	var _ plugin.Plugin = (*BundlePlugin)(nil)
	var _ plugin.GRPCPlugin = (*BundlePlugin)(nil)
}
