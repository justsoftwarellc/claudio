package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/pkg/jsonmessage"
)

// BuildFile is one file to place in a Docker build context, keyed by its
// path within that context (e.g. "Dockerfile", "entrypoint.sh").
type BuildFile struct {
	Name     string
	Contents []byte
}

// BuildSpec is everything BuildImage needs to run one `docker build`
// equivalent. Files must include a "Dockerfile" entry — Dockerfile names
// which file within Files plays that role (usually "Dockerfile", but the
// repo-layer build in ROD-96's per-repo image path uses a distinct name
// so it never collides with an escape-hatch file of the same name copied
// alongside it into the same context).
type BuildSpec struct {
	Files      []BuildFile
	Dockerfile string
	Tags       []string
	BuildArgs  map[string]string

	// Progress receives the build's own textual output (the same stream
	// `docker build` prints to a terminal) as it happens — long-running
	// and network-dependent, so a caller can show something rather than
	// sit silent (mirrors core.ProgressFunc's rationale for create).
	// nil means discard.
	Progress io.Writer
}

// ImageExists reports whether image is present in the local image store.
// Used by core's create path to raise a friendly "run claudio image
// build" error instead of surfacing Docker's own "no such image" text
// (ROD-96) — checked ahead of CreateAndStart so the better message is
// raised at the right point, rather than parsed out of Docker's error.
func ImageExists(ctx context.Context, host, image string) (bool, error) {
	cli, err := newClient(ctx, host)
	if err != nil {
		return false, err
	}
	defer cli.Close()

	if _, err := cli.ImageInspect(ctx, image); err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("engine: inspect image %s: %w", image, err)
	}
	return true, nil
}

// BuildImage runs a Docker build from an in-memory context (spec.Files)
// and tags the result with every name in spec.Tags. The context is built
// as a tar stream in memory rather than written to a temp directory on
// disk: every input is already in memory (an embedded Dockerfile, or a
// generated repo-layer one), and Docker's build API takes the context as
// a tar stream regardless, so writing it to disk first would only be
// extra I/O with nothing to show for it.
//
// Docker's own layer cache applies exactly as it would for `docker build`
// run twice in a row from the CLI — nothing here disables or bypasses it
// (no NoCache), so a second build of an unchanged Dockerfile is fast.
func BuildImage(ctx context.Context, host string, spec BuildSpec) error {
	cli, err := newClient(ctx, host)
	if err != nil {
		return err
	}
	defer cli.Close()

	tarCtx, err := tarBuildContext(spec.Files)
	if err != nil {
		return fmt.Errorf("engine: build image context: %w", err)
	}

	buildArgs := make(map[string]*string, len(spec.BuildArgs))
	for k, v := range spec.BuildArgs {
		v := v
		buildArgs[k] = &v
	}

	resp, err := cli.ImageBuild(ctx, tarCtx, build.ImageBuildOptions{
		Tags:       spec.Tags,
		Dockerfile: spec.Dockerfile,
		BuildArgs:  buildArgs,
		Remove:     true, // remove intermediate containers on success, same as `docker build`'s own default
	})
	if err != nil {
		return fmt.Errorf("engine: build image: %w", err)
	}
	defer resp.Body.Close()

	progress := spec.Progress
	if progress == nil {
		progress = io.Discard
	}
	// DisplayJSONMessagesStream, not a bare io.Copy: ImageBuild's HTTP call
	// succeeding only means the daemon accepted the request and started
	// streaming — a broken Dockerfile (e.g. a bad RUN command) surfaces as
	// an errorDetail message *within* that stream, not as a non-2xx
	// response or a Go error from ImageBuild itself. Verified empirically:
	// a build with a deliberately failing RUN returned a 200 with a
	// stream that ends in {"errorDetail":...}; only this call surfaces
	// that as a Go error the caller can act on.
	if err := jsonmessage.DisplayJSONMessagesStream(resp.Body, progress, 0, false, nil); err != nil {
		return fmt.Errorf("engine: build image: %w", err)
	}
	return nil
}

// tarBuildContext packages files into an uncompressed tar stream, the
// format Docker's build API accepts as a build context. Uncompressed
// (not tar.gz) because everything here is already small (a Dockerfile
// plus at most one escape-hatch file) and BuildKit re-reads it locally,
// not over a slow network link — gzip would only cost CPU for no
// measurable transfer win at this size.
func tarBuildContext(files []BuildFile) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		hdr := &tar.Header{
			Name: f.Name,
			Mode: 0o644,
			Size: int64(len(f.Contents)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("tar header for %s: %w", f.Name, err)
		}
		if _, err := tw.Write(f.Contents); err != nil {
			return nil, fmt.Errorf("tar write %s: %w", f.Name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar: %w", err)
	}
	return &buf, nil
}
