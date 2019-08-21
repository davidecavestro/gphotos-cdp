package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

var (
	nItemsFlag = flag.Int("n", -1, "number of items to download. If negative, get them all.")
	devFlag    = flag.Bool("dev", false, "dev mode. we reuse the same session dir (/tmp/gphotos-cdp), so we don't have to auth at every run.")
	dlDirFlag  = flag.String("dldir", "", "where to (temporarily) write the downloads. defaults to $HOME/Downloads/gphotos-cdp.")
)

func main() {
	flag.Parse()
	if *nItemsFlag == 0 {
		return
	}
	s, err := NewSession()
	if err != nil {
		log.Fatal(err)
	}
	defer s.Shutdown()

	log.Printf("Session Dir: %v", s.profileDir)

	if err := s.cleanDlDir(); err != nil {
		log.Fatal(err)
	}

	ctx, cancel := s.NewContext()
	defer cancel()

	var outerBefore string

	// login phase
	if err := chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			log.Printf("pre-navigate")
			return nil
		}),
		chromedp.Navigate("https://photos.google.com/"),
		// chromedp.Sleep(30000*time.Millisecond),
		chromedp.Sleep(5000*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			log.Printf("post-navigate")
			return nil
		}),
		chromedp.OuterHTML("html>body", &outerBefore),
		chromedp.ActionFunc(func(ctx context.Context) error {
			log.Printf("Source is %d bytes", len(outerBefore))
			return nil
		}),
	); err != nil {
		log.Fatal(err)
	}

	navEnd := func(ctx context.Context) error {
		keyEnd, ok := kb.Keys['\u0305']
		if !ok {
			return errors.New("no End key")
		}

		down := input.DispatchKeyEventParams{
			Key:  keyEnd.Key,
			Code: keyEnd.Code,
			// Some github issue says to remove NativeVirtualKeyCode, but it does not change anything.
			NativeVirtualKeyCode:  keyEnd.Native,
			WindowsVirtualKeyCode: keyEnd.Windows,
			Type:                  input.KeyDown,
		}
		if runtime.GOOS == "darwin" {
			down.NativeVirtualKeyCode = 0
		}
		up := down
		up.Type = input.KeyUp

		for _, ev := range []*input.DispatchKeyEventParams{&down, &up} {
			log.Printf("Event: %+v", *ev)
			if err := ev.Do(ctx); err != nil {
				return err
			}
		}
		time.Sleep(5 * time.Second)
		return nil
	}

	download := func(ctx context.Context) (string, error) {
		dir := s.dlDir
		keyD, ok := kb.Keys['D']
		if !ok {
			log.Fatal("NO D KEY")
		}

		down := input.DispatchKeyEventParams{
			Key:  keyD.Key,
			Code: keyD.Code,
			// Some github issue says to remove NativeVirtualKeyCode, but it does not change anything.
			NativeVirtualKeyCode:  keyD.Native,
			WindowsVirtualKeyCode: keyD.Windows,
			Type:                  input.KeyDown,
			Modifiers:             input.ModifierShift,
		}
		if runtime.GOOS == "darwin" {
			down.NativeVirtualKeyCode = 0
		}
		up := down
		up.Type = input.KeyUp

		for _, ev := range []*input.DispatchKeyEventParams{&down, &up} {
			log.Printf("Event: %+v", *ev)
			if err := ev.Do(ctx); err != nil {
				return "", err
			}
		}

		started := false
		endTimeout := time.Now().Add(30 * time.Second)
		startTimeout := time.Now().Add(5 * time.Second)
		tick := 500 * time.Millisecond
		for {
			time.Sleep(tick)
			if time.Now().After(endTimeout) {
				return "", fmt.Errorf("timeout while downloading in %q", dir)
			}

			if !started && time.Now().After(startTimeout) {
				return "", fmt.Errorf("downloading in %q took too long to start", dir)
			}
			entries, err := ioutil.ReadDir(dir)
			if err != nil {
				return "", err
			}
			var fileEntries []string
			for _, v := range entries {
				if v.IsDir() {
					continue
				}
				fileEntries = append(fileEntries, v.Name())
			}
			if len(fileEntries) < 1 {
				continue
			}
			if !started {
				if len(fileEntries) > 0 {
					started = true
				}
				continue
			}
			if len(fileEntries) > 1 {
				return "", fmt.Errorf("more than one file (%d) in download dir %q", len(fileEntries), dir)
			}
			if !strings.HasSuffix(fileEntries[0], ".crdownload") {
				// download is over
				return fileEntries[0], nil
			}
		}
	}

	mvDl := func(dlFile string) func(ctx context.Context) error {
		return func(ctx context.Context) error {
			dir, err := ioutil.TempDir(s.dlDir, "")
			if err != nil {
				return err
			}
			if err := os.Rename(filepath.Join(s.dlDir, dlFile), filepath.Join(dir, dlFile)); err != nil {
				return err
			}
			return nil
		}
	}

	dlAndMove := func(ctx context.Context) error {
		var err error
		dlFile, err := download(ctx)
		if err != nil {
			return err
		}
		return mvDl(dlFile)(ctx)
	}

	firstNav := func(ctx context.Context) error {
		chromedp.KeyEvent(kb.ArrowRight).Do(ctx)
		log.Printf("sent key")
		chromedp.Sleep(500 * time.Millisecond).Do(ctx)
		chromedp.KeyEvent("\n").Do(ctx)
		chromedp.Sleep(500 * time.Millisecond).Do(ctx)
		return nil
	}

	navRight := func(ctx context.Context) error {
		chromedp.KeyEvent(kb.ArrowRight).Do(ctx)
		log.Printf("sent key")
		chromedp.Sleep(5000 * time.Millisecond).Do(ctx)
		chromedp.WaitReady("body", chromedp.ByQuery)
		return nil
	}

	navLeft := func(ctx context.Context) error {
		chromedp.KeyEvent(kb.ArrowRight).Do(ctx)
		log.Printf("sent key")
		chromedp.Sleep(5000 * time.Millisecond).Do(ctx)
		chromedp.WaitReady("body", chromedp.ByQuery)
		return nil
	}

	navN := func(direction string, N int) func(context.Context) error {
		n := 0
		return func(ctx context.Context) error {
			if direction != "left" && direction != "right" {
				return errors.New("wrong direction, pun intended")
			}
			if N == 0 {
				return nil
			}
			for {
				if N > 0 && n >= N {
					break
				}
				if direction == "right" {
					if err := navRight(ctx); err != nil {
						return err
					}
				} else {
					if err := navLeft(ctx); err != nil {
						return err
					}
				}
				// TODO(mpl): deal with getting the very last photo to properly exit that loop when N < 0.
				if err := dlAndMove(ctx); err != nil {
					return err
				}
				n++
			}
			return nil
		}
	}

	if err := chromedp.Run(ctx,
		chromedp.ActionFunc(navEnd),
		page.SetDownloadBehavior(page.SetDownloadBehaviorBehaviorAllow).WithDownloadPath(s.dlDir),
		// TODO(mpl): add policy func over photo URL, which decides what we do with the downloaded file. default policy is storing it on disk.
		chromedp.Navigate("https://photos.google.com/"),
		chromedp.Sleep(5000*time.Millisecond),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			log.Printf("body is ready")
			return nil
		}),
		// For some reason, I need to do a pagedown before, for the end key to work...
		chromedp.KeyEvent(kb.PageDown),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.KeyEvent(kb.End),
		chromedp.Sleep(5000*time.Millisecond),
		// TODO(mpl): do it the smart(er) way: nav right in photo view until the URL does not change anymore. Or something.
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.KeyEvent(kb.ArrowRight),
		chromedp.ActionFunc(firstNav),
		chromedp.ActionFunc(dlAndMove),
		chromedp.ActionFunc(navN("left", *nItemsFlag-1)),
	); err != nil {
		log.Fatal(err)
	}
	fmt.Println("OK")

	// Next: keys
	// https://github.com/chromedp/chromedp/issues/400
	// https://godoc.org/github.com/chromedp/chromedp/kb

	_ = firstNav

}

type Session struct {
	parentContext context.Context
	parentCancel  context.CancelFunc
	dlDir         string
	profileDir    string
}

func NewSession() (*Session, error) {
	var dir string
	if *devFlag {
		dir = filepath.Join(os.TempDir(), "gphotos-cdp")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	} else {
		var err error
		dir, err = ioutil.TempDir("", "gphotos-cdp")
		if err != nil {
			return nil, err
		}
	}
	dlDir := *dlDirFlag
	if dlDir == "" {
		dlDir = filepath.Join(os.Getenv("HOME"), "Downloads", "gphotos-cdp")
	}
	if err := os.MkdirAll(dlDir, 0700); err != nil {
		return nil, err
	}
	s := &Session{
		profileDir: dir,
		dlDir:      dlDir,
	}
	return s, nil
}

func (s *Session) NewContext() (context.Context, context.CancelFunc) {
	ctx, cancel := chromedp.NewExecAllocator(context.Background(),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.UserDataDir(s.profileDir),

		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("enable-features", "NetworkService,NetworkServiceInProcess"),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-breakpad", true),
		chromedp.Flag("disable-client-side-phishing-detection", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-features", "site-per-process,TranslateUI,BlinkGenPropertyTrees"),
		chromedp.Flag("disable-hang-monitor", true),
		chromedp.Flag("disable-ipc-flooding-protection", true),
		chromedp.Flag("disable-popup-blocking", true),
		chromedp.Flag("disable-prompt-on-repost", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("force-color-profile", "srgb"),
		chromedp.Flag("metrics-recording-only", true),
		chromedp.Flag("safebrowsing-disable-auto-update", true),
		chromedp.Flag("enable-automation", true),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("use-mock-keychain", true),
	)
	s.parentContext = ctx
	s.parentCancel = cancel
	ctx, cancel = chromedp.NewContext(s.parentContext)
	return ctx, cancel
}

func (s *Session) Shutdown() {
	s.parentCancel()
}

func (s *Session) cleanDlDir() error {
	if s.dlDir == "" {
		return nil
	}
	entries, err := ioutil.ReadDir(s.dlDir)
	if err != nil {
		return err
	}
	for _, v := range entries {
		if v.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(s.dlDir, v.Name())); err != nil {
			return err
		}
	}
	return nil
}