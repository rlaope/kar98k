# internal/rpc/proto

## Source of truth

`kar.proto` defines the KarMaster gRPC service used for distributed mode (#52).

## Generated files

`kar.pb.go` and `kar_grpc.pb.go` were **hand-crafted** because `protoc` was not
available at the time of initial implementation. The message types and gRPC
stubs follow `google.golang.org/grpc v1.80` and `google.golang.org/protobuf`
conventions exactly, so they are functionally equivalent to `protoc` output.

When `protoc` becomes available, regenerate with:

```bash
make proto
```

This will overwrite both files with proper generated output, including the raw
file descriptor bytes that enable full proto reflection.

## Installing protoc

```bash
# macOS
brew install protobuf
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Linux
apt-get install -y protobuf-compiler
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```
