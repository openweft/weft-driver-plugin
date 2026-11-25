package weftdriverplugin

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/openweft/weft-driver-plugin/driverpb"
	drivers "github.com/openweft/weft-drivers"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func testToken() []byte {
	t := make([]byte, drivers.BootTokenLen)
	for i := range t {
		t[i] = byte(i + 1)
	}
	return t
}

func TestStartVMWithBootToken_RoundTrip(t *testing.T) {
	fake := &fakeDriver{}
	set := dialSet(t, fake)
	bts, ok := set.Hypervisor.(drivers.BootTokenStarter)
	if !ok {
		t.Fatal("client does not implement drivers.BootTokenStarter")
	}
	if err := bts.StartVMWithBootToken(context.Background(), "vm", testToken()); err != nil {
		t.Fatalf("StartVMWithBootToken: %v", err)
	}
	if !bytes.Equal(fake.lastToken, testToken()) {
		t.Errorf("token did not reach the driver intact: %x", fake.lastToken)
	}
}

func TestStartVMWithBootToken_DriverErrorPropagates(t *testing.T) {
	set := dialSet(t, &fakeDriver{failWith: drivers.ErrNotFound})
	err := set.Hypervisor.(drivers.BootTokenStarter).StartVMWithBootToken(context.Background(), "vm", testToken())
	if !errors.Is(err, drivers.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestStartVMWithBootToken_ClientRejectsBadLength(t *testing.T) {
	fake := &fakeDriver{}
	set := dialSet(t, fake)
	bts := set.Hypervisor.(drivers.BootTokenStarter)
	for _, n := range []int{0, 31, 33} {
		err := bts.StartVMWithBootToken(context.Background(), "vm", make([]byte, n))
		if !errors.Is(err, drivers.ErrInvalidBootToken) {
			t.Errorf("len %d: err = %v", n, err)
		}
	}
	if fake.lastToken != nil {
		t.Error("a rejected token reached the driver")
	}
}

// dialRaw serves impl and returns the generated gRPC client, bypassing the
// host-side validation.
func dialRaw(t *testing.T, impl drivers.HypervisorDriver) driverpb.HypervisorClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	driverpb.RegisterHypervisorServer(s, &hypervisorServer{impl: impl})
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return driverpb.NewHypervisorClient(conn)
}

func TestStartVMWithBootToken_ServerRejectsBadLength(t *testing.T) {
	fake := &fakeDriver{}
	c := dialRaw(t, fake)
	bad := bytes.Repeat([]byte{0xAB}, 5)
	_, err := c.StartVMWithBootToken(context.Background(), &driverpb.StartVMWithBootTokenRequest{VmUuid: "vm", BootToken: bad})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (%v)", status.Code(err), err)
	}
	if strings.Contains(err.Error(), "abab") || strings.Contains(err.Error(), "171") {
		t.Errorf("error leaks token: %v", err)
	}
	if fake.lastToken != nil {
		t.Error("a rejected token reached the driver")
	}
}

// noTokenDriver is a HypervisorDriver without BootTokenStarter.
type noTokenDriver struct{ drivers.HypervisorDriver }

func TestStartVMWithBootToken_ServerUnimplemented(t *testing.T) {
	c := dialRaw(t, noTokenDriver{})
	_, err := c.StartVMWithBootToken(context.Background(), &driverpb.StartVMWithBootTokenRequest{VmUuid: "vm", BootToken: testToken()})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("code = %v, want Unimplemented (%v)", status.Code(err), err)
	}
}
