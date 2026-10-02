package accountlifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestOwnerCleanupRunsAfterDrainAndRetriesOnRestart(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		name := "disable"
		if deleting {
			name = "delete"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			us, err := users.New(filepath.Join(root, "users.json"), filepath.Join(root, "users"))
			if err != nil {
				t.Fatal(err)
			}
			u, err := us.Create("owner", "test-password", false)
			if err != nil {
				t.Fatal(err)
			}
			device, _, err := us.CreateDevice(u.ID, "phone")
			if err != nil {
				t.Fatal(err)
			}
			q := quota.New(root)
			reg := filesvc.NewRegistry(us, q)
			caps := capsule.New(filepath.Join(root, "caps"))
			up := upload.New(filepath.Join(root, "uploads"), reg.For, q, us.Count)
			m := New(us, reg, caps, up)
			entered, release, ok := reg.Enter(context.Background(), u.ID)
			if !ok {
				t.Fatal("initial admission")
			}
			cleanup := make(chan string, 2)
			calls := 0
			m.SetOwnerCleanup(func(ctx context.Context, owner string) error {
				calls++
				cleanup <- owner
				if _, release, ok := reg.Enter(ctx, owner); ok {
					release()
					return errors.New("cleanup before owner admission blocked")
				}
				if _, ok := us.ActiveDevice(owner, device.ID); ok {
					return errors.New("cleanup before credentials invalidated")
				}
				if _, err := os.Stat(filepath.Join(root, "users", owner)); err != nil {
					return err
				}
				if calls == 1 {
					return errors.New("injected cleanup failure")
				}
				return nil
			})
			result := make(chan error, 1)
			go func() {
				if deleting {
					result <- m.Delete(context.Background(), u.ID)
				} else {
					result <- m.SetDisabled(context.Background(), u.ID, true)
				}
			}()
			<-entered.Done()
			select {
			case <-cleanup:
				t.Fatal("cleanup raced active lease")
			default:
			}
			release()
			if err := <-result; err == nil {
				t.Fatal("cleanup failure ignored")
			}
			if <-cleanup != u.ID {
				t.Fatal("foreign owner cleanup")
			}
			state, ok := us.User(u.ID)
			if !ok || !state.Disabled {
				t.Fatal("failed cleanup lost pending account")
			}
			if deleting && state.DeleteError != "mobile staging" || !deleting && state.DisableError != "mobile staging" {
				t.Fatal("cleanup stage not durable")
			}
			if err := m.ResumePending(context.Background()); err != nil {
				t.Fatal(err)
			}
			if <-cleanup != u.ID || calls != 2 {
				t.Fatal("cleanup not retried")
			}
			state, ok = us.User(u.ID)
			if deleting && ok {
				t.Fatal("deleted account retained")
			}
			if !deleting && (!ok || state.DisablePending || state.DisableError != "") {
				t.Fatal("disable cleanup incomplete")
			}
		})
	}
}
