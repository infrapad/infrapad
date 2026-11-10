module github.com/infrapad/infrapad/cli

go 1.26.4

require (
	github.com/infrapad/infrapad/proto/gen/go v0.0.0
	github.com/infrapad/infrapad/shared v0.0.0
	github.com/spf13/cobra v1.10.2
	go.yaml.in/yaml/v3 v3.0.5
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260810153831-ec0a7760b754 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260807164820-c8921c73eeea // indirect
	google.golang.org/grpc v1.83.0 // indirect
)

replace github.com/infrapad/infrapad/proto/gen/go => ../proto/gen/go

replace github.com/infrapad/infrapad/shared => ../shared
