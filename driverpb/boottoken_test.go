package driverpb

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestStartVMWithBootTokenRequestGetters(t *testing.T) {
	var nilReq *StartVMWithBootTokenRequest
	if nilReq.GetVmUuid() != "" || nilReq.GetBootToken() != nil {
		t.Error("nil receiver must yield zero values")
	}
	r := &StartVMWithBootTokenRequest{VmUuid: "vm", BootToken: []byte{1, 2}}
	if r.GetVmUuid() != "vm" || len(r.GetBootToken()) != 2 {
		t.Error("getters do not return the fields")
	}
	if r.String() == "" || r.ProtoReflect() == nil {
		t.Error("reflection broken")
	}
	if nilReq.ProtoReflect().IsValid() {
		t.Error("nil message must reflect as invalid")
	}
	r.Reset()
	if r.GetVmUuid() != "" {
		t.Error("Reset did not clear")
	}
}

func TestStartVMWithBootTokenHandler(t *testing.T) {
	srv := UnimplementedHypervisorServer{}
	ctx := context.Background()

	// decode failure is returned as is
	boom := errors.New("decode")
	if _, err := _Hypervisor_StartVMWithBootToken_Handler(srv, ctx, func(any) error { return boom }, nil); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	ok := func(any) error { return nil }
	// no interceptor: straight to the (unimplemented) method
	if _, err := _Hypervisor_StartVMWithBootToken_Handler(srv, ctx, ok, nil); status.Code(err) != codes.Unimplemented {
		t.Errorf("err = %v", err)
	}
	// interceptor is invoked with the full method name
	called := ""
	ic := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		called = info.FullMethod
		return h(ctx, req)
	}
	if _, err := _Hypervisor_StartVMWithBootToken_Handler(srv, ctx, ok, ic); status.Code(err) != codes.Unimplemented {
		t.Errorf("err = %v", err)
	}
	if called != Hypervisor_StartVMWithBootToken_FullMethodName {
		t.Errorf("interceptor saw %q", called)
	}
}
