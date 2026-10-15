package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/client"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the server and report database problems",
	RunE: withClient(func(cmd *cobra.Command, a *app, c *client.Client) error {
		out := cmd.OutOrStdout()
		health, err := c.Health(cmd.Context())
		if err != nil {
			return fmt.Errorf("server unreachable at %s: %w", a.cfg.ServerURL, err)
		}
		fmt.Fprintf(out, "server: ok (%s)\n", a.cfg.ServerURL)
		fmt.Fprintf(out, "versions: client %s, server %s\n", Version, cmp.Or(health.Version, "unknown"))
		tooOld := versionBelow(Version, health.MinClientVersion)
		if tooOld {
			fmt.Fprintf(out, "this client is older than %s, the oldest the server supports: upgrade it\n", health.MinClientVersion)
		}
		me, err := c.Me(cmd.Context())
		if client.IsCode(err, api.CodeUnauthorized) {
			return errors.New("the server refused BELLEK_TOKEN: the key is unknown, revoked or expired")
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "key: %s (%s%s)\nuser: %s <%s>\nworkspace: %s (%s)\n", me.Key.Name, api.KeyPrefix, me.Key.Prefix,
			me.User.Name, me.User.Email, me.Workspace.Name, me.Workspace.Kind)
		problems := tooOld
		if me.Workspace.Role == api.RoleAdmin {
			rep, err := c.Doctor(cmd.Context())
			if err != nil {
				return err
			}
			printDoctor(out, rep)
			problems = problems || rep.OrphanEdges > 0
		} else {
			fmt.Fprintln(out, "counts: only an admin of the workspace reads them")
		}
		if problems {
			return errors.New("doctor found problems")
		}
		return nil
	}),
}

func printDoctor(out io.Writer, rep api.DoctorReport) {
	fmt.Fprintf(out, "nodes: %d, edges: %d, events: %d\n", rep.Nodes, rep.Edges, rep.Events)
	fmt.Fprintf(out, "orphan edges: %d\nunresolved forward references: %d\nexpired leases: %d\nstale sessions: %d\n",
		rep.OrphanEdges, rep.ForwardRefs, rep.ExpiredLeases, rep.StaleSessions)
	if rep.MaxEventsProject == "" {
		fmt.Fprintln(out, "largest project: none")
	} else {
		fmt.Fprintf(out, "largest project: %s with %d events\n", rep.MaxEventsProject, rep.MaxProjectEvents)
	}
	fmt.Fprintf(out, "retainable events: %d\n", rep.RetainableEvents)
	if rep.MaxProjectEvents > api.RetentionTrigger/2 {
		fmt.Fprintf(out, "project %s has %d events, retention trigger is one million\n", rep.MaxEventsProject, rep.MaxProjectEvents)
	}
}

func versionBelow(v, min string) bool {
	var a, b [3]int
	if _, err := fmt.Sscanf(v, "v%d.%d.%d", &a[0], &a[1], &a[2]); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(min, "v%d.%d.%d", &b[0], &b[1], &b[2]); err != nil {
		return false
	}
	return slices.Compare(a[:], b[:]) < 0
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
