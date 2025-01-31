module github.com/openllb/hlb

go 1.16

require (
	github.com/alecthomas/participle/v2 v2.0.0-alpha7.0.20211230082035-5a357f57e525
	github.com/chzyer/readline v0.0.0-20180603132655-2972be24d48e
	github.com/containerd/console v1.0.3
	github.com/containerd/containerd v1.6.1
	github.com/creachadair/jrpc2 v0.26.1
	github.com/creack/pty v1.1.11
	github.com/docker/buildx v0.8.1-0.20220315021307-3adca1c17db9
	github.com/docker/cli v20.10.12+incompatible
	github.com/docker/distribution v2.8.0+incompatible
	github.com/docker/docker v20.10.7+incompatible
	github.com/fvbommel/sortorder v1.0.2 // indirect
	github.com/google/go-dap v0.6.0
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/lithammer/dedent v1.1.0
	github.com/logrusorgru/aurora v0.0.0-20191116043053-66b7ad493a23
	github.com/mattn/go-isatty v0.0.12
	github.com/moby/buildkit v0.10.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.2-0.20211117181255-693428a734f5
	github.com/openllb/doxygen-parser v0.0.0-20201031162929-e0b5cceb2d0c
	github.com/pkg/errors v0.9.1
	github.com/sourcegraph/go-lsp v0.0.0-20200117082640-b19bb38222e2
	github.com/stretchr/testify v1.7.0
	github.com/theupdateframework/notary v0.7.0 // indirect
	github.com/tonistiigi/fsutil v0.0.0-20220115021204-b19f7f9cb274
	github.com/urfave/cli/v2 v2.1.1
	github.com/xlab/treeprint v1.0.0
	golang.org/x/crypto v0.0.0-20211202192323-5770296d904e
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	golang.org/x/sys v0.0.0-20220209214540-3681064d5158
	google.golang.org/grpc v1.44.0
)

replace (
	github.com/docker/cli => github.com/docker/cli v20.10.3-0.20220226190722-8667ccd1124c+incompatible
	github.com/docker/docker => github.com/docker/docker v20.10.3-0.20220121014307-40bb9831756f+incompatible
	github.com/sourcegraph/go-lsp => github.com/radeksimko/go-lsp v0.0.0-20200223162147-9f2c54f29c9f
)
