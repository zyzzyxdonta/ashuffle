package integration_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"testing"
	"time"

	"ashuffle"
	"mpd"
)

const ashuffleBin = "/ashuffle/build/ashuffle"

const (
	// must be less than waitMax
	waitBackoff = 20 * time.Millisecond
	// 100ms, because we want ashuffle operations to be imperceptible.
	waitMax = 100 * time.Millisecond
)

func panicf(format string, params ...interface{}) {
	panic(fmt.Sprintf(format, params...))
}

// Optimistically wait for some condition to be true. Sometimes, we need to
// wait for ashuffle to perform some action, and since this is a test, it
// may or may not successfully perform that action. To avoid putting in
// load-bearing sleeps that slow down the test, and make it more fragile, we
// can use this function instead. Ideally, it completes instantly, but it
// may take a few hundred millis before ashuffle actually gets around to
// doing what it is supposed to do.
func tryWaitFor(cond func() bool) {
	for {
		select {
		case <-time.After(waitMax):
			log.Printf("giving up after waiting %s", waitMax)
			return
		case <-time.Tick(waitBackoff):
			if cond() {
				return
			}
		}
	}
}

func TestMain(m *testing.M) {
	// compile ashuffle
	origDir, err := os.Getwd()
	if err != nil {
		panicf("failed to getcwd: %v", err)
	}

	if err := os.Chdir("/ashuffle"); err != nil {
		panicf("failed to chdir to /ashuffle: %v", err)
	}

	fmt.Println("===> Running MESON")
	mesonCmd := exec.Command("meson", "build")
	mesonCmd.Stdout = os.Stdout
	mesonCmd.Stderr = os.Stderr
	if err := mesonCmd.Run(); err != nil {
		panicf("failed to run meson for ashuffle: %v", err)
	}

	fmt.Println("===> Building ashuffle")
	ninjaCmd := exec.Command("ninja", "-C", "build", "ashuffle")
	ninjaCmd.Stdout = os.Stdout
	ninjaCmd.Stderr = os.Stderr
	if err := ninjaCmd.Run(); err != nil {
		panicf("failed to build ashuffle: %v", err)
	}

	if err := os.Chdir(origDir); err != nil {
		panicf("failed to rest workdir: %v", err)
	}

	os.Exit(m.Run())
}

// Basic test, just to make sure we can start MPD and ashuffle.
func TestStartup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mpdi, err := mpd.New(ctx, &mpd.Options{LibraryRoot: "/music"})
	if err != nil {
		t.Fatalf("Failed to create new MPD instance: %v", err)
	}
	ashuffle, err := ashuffle.New(ctx, ashuffleBin, &ashuffle.Options{
		MPDAddress: mpdi,
	})
	if err != nil {
		t.Fatalf("Failed to create new ashuffle instance")
	}

	if err := ashuffle.Shutdown(); err != nil {
		t.Errorf("ashuffle did not shut down cleanly: %v", err)
	}
	mpdi.Shutdown()
}

func TestShuffleOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mpdi, err := mpd.New(ctx, &mpd.Options{LibraryRoot: "/music"})
	if err != nil {
		t.Fatalf("failed to create new MPD instance: %v", err)
	}
	as, err := ashuffle.New(ctx, ashuffleBin, &ashuffle.Options{
		MPDAddress: mpdi,
		Args:       []string{"-o", "3"},
	})
	if err != nil {
		t.Fatalf("failed to create new ashuffle instance")
	}

	// Wait for ashuffle to exit.
	if err := as.Shutdown(ashuffle.ShutdownSoft); err != nil {
		t.Errorf("ashuffle did not shut down cleanly: %v", err)
	}

	if state := mpdi.PlayState(); state != mpd.StateStop {
		t.Errorf("want mpd state stop, got: %v", state)
	}

	if queueLen := len(mpdi.Queue()); queueLen != 3 {
		t.Errorf("want mpd queue len 3, got %d", queueLen)
	}

	if !mpdi.IsOk() {
		t.Errorf("mpd communication error: %v", mpdi.Errors)
	}

	mpdi.Shutdown()
}

// Starting up ashuffle in a clean MPD instance. The "default" workflow. Then
// we skip a song, and make sure ashuffle enqueues another song.
func TestBasic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mpdi, err := mpd.New(ctx, &mpd.Options{LibraryRoot: "/music"})
	if err != nil {
		t.Fatalf("failed to create mpd instance: %v", err)
	}
	ashuffle, err := ashuffle.New(ctx, ashuffleBin, &ashuffle.Options{
		MPDAddress: mpdi,
	})

	// Wait for ashuffle to startup, and start playing a song.
	tryWaitFor(func() bool { return mpdi.PlayState() == mpd.StatePlay })

	if state := mpdi.PlayState(); state != mpd.StatePlay {
		t.Errorf("[before skip] want mpd state play, got %v", state)
	}
	if queueLen := len(mpdi.Queue()); queueLen != 1 {
		t.Errorf("[before skip] want mpd queue len == 1, got len %d", queueLen)
	}
	if pos := mpdi.QueuePos(); pos != 0 {
		t.Errorf("[before skip] want mpd queue pos == 0, got %d", pos)
	}

	// Skip a track, ashuffle should enqueue another song, and keep playing.
	mpdi.Next()
	// Give ashuffle some time to try and react, otherwise the test always
	// fails.
	tryWaitFor(func() bool { return mpdi.PlayState() == mpd.StatePlay })

	if state := mpdi.PlayState(); state != mpd.StatePlay {
		t.Errorf("[after skip] want mpd state play, got %v", state)
	}
	if queueLen := len(mpdi.Queue()); queueLen != 2 {
		t.Errorf("[after skip] want mpd queue len == 2, got len %d", queueLen)
	}
	if pos := mpdi.QueuePos(); pos != 1 {
		t.Errorf("[after skip] want mpd queue pos == 1, got %d", pos)
	}

	if !mpdi.IsOk() {
		t.Errorf("mpd communication error: %v", mpdi.Errors)
	}
	if err := ashuffle.Shutdown(); err != nil {
		t.Errorf("ashuffle did not shut down cleanly: %v", err)
	}
	mpdi.Shutdown()
}