package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
)

// cmdPorts implements `claudio ports <id> [--add container] [--remove
// container]`. With no flags, shows the instance's current port
// mappings (docs/architecture.md §6.1's SERVICE/CONTAINER/HOST/SOURCE
// table). --add and --remove only ever touch the store — Docker cannot
// change a running container's published ports — so both print an
// explicit note that the change needs `claudio restart` to take effect.
func cmdPorts(ctx context.Context, args []string) int {
	var rest []string
	var addPort, removePort int
	var doAdd, doRemove bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--add":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio ports: --add requires a container port")
				return 1
			}
			p, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "claudio ports: --add: %q is not a valid port\n", args[i])
				return 1
			}
			addPort, doAdd = p, true
		case "--remove":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio ports: --remove requires a container port")
				return 1
			}
			p, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "claudio ports: --remove: %q is not a valid port\n", args[i])
				return 1
			}
			removePort, doRemove = p, true
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "claudio ports: unknown flag %q\n", args[i])
				return 1
			}
			rest = append(rest, args[i])
		}
	}
	if doAdd && doRemove && addPort == removePort {
		fmt.Fprintln(os.Stderr, "claudio ports: --add and --remove name the same port")
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio ports:", describeErr(err))
		return 1
	}
	defer c.Close()

	idOrName, ok := resolveIDWithClient(ctx, c, rest, "claudio ports")
	if !ok {
		return 1
	}

	if doAdd {
		hostPort, err := c.AddPort(ctx, idOrName, addPort)
		if err != nil {
			fmt.Fprintln(os.Stderr, "claudio ports:", describeErr(err))
			return 1
		}
		fmt.Printf("Reserved container port %d -> host port %d.\n", addPort, hostPort)
		fmt.Printf("Not yet published on the running container — run `claudio restart %s` to apply it,\n", idOrName)
		fmt.Println("then start the app inside the container again so it listens on the port.")
	}
	if doRemove {
		if err := c.RemovePort(ctx, idOrName, removePort); err != nil {
			fmt.Fprintln(os.Stderr, "claudio ports:", describeErr(err))
			return 1
		}
		fmt.Printf("Released container port %d.\n", removePort)
		fmt.Printf("Still published on the running container until you run `claudio restart %s`.\n", idOrName)
	}

	inst, err := c.Status(ctx, idOrName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio ports:", describeErr(err))
		return 1
	}
	if len(inst.Ports) == 0 {
		fmt.Println("No ports mapped.")
		return 0
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tCONTAINER\tHOST\tSOURCE")
	for _, p := range inst.Ports {
		source := string(p.Source)
		if p.DetectedFrom != nil {
			source = fmt.Sprintf("%s (%s)", source, *p.DetectedFrom)
		}
		fmt.Fprintf(tw, "%s\t%d\thttp://127.0.0.1:%d\t%s\n", p.ServiceName, p.ContainerPort, p.HostPort, source)
	}
	tw.Flush()
	return 0
}
