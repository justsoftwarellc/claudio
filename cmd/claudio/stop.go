// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"fmt"
	"os"
)

// cmdStop implements `claudio stop <id>`.
func cmdStop(ctx context.Context, args []string) int {

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio stop:", describeErr(err))
		return 1
	}
	defer c.Close()

	idOrName, ok := resolveIDWithClient(ctx, c, args, "claudio stop")
	if !ok {
		return 1
	}

	if err := c.Stop(ctx, idOrName); err != nil {
		fmt.Fprintln(os.Stderr, "claudio stop:", describeErr(err))
		return 1
	}

	fmt.Printf("Stopped %s\n", idOrName)
	return 0
}
