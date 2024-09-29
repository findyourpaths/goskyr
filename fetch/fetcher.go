package fetch

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/chromedp"
	"github.com/findyourpaths/goskyr/config"
	"github.com/findyourpaths/goskyr/types"
	"github.com/findyourpaths/goskyr/utils"
	"golang.org/x/exp/slog"
)

type FetchOpts struct {
	Interaction []*types.Interaction
}

// A Fetcher allows to fetch the content of a web page
type Fetcher interface {
	Fetch(url string, opts FetchOpts) (string, error)
}

// The StaticFetcher fetches static page content
type StaticFetcher struct {
	UserAgent string
}

func (s *StaticFetcher) Fetch(url string, opts FetchOpts) (string, error) {
	log.Printf("StaticFetcher.Fetch(url: %q, opts: %#v)", url, opts)
	// s.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36"
	s.UserAgent = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/51.0.2704.103 Safari/537.36"
	slog.Debug("fetching page", slog.String("fetcher", "static"), slog.String("url", url), slog.String("user-agent", s.UserAgent))
	var resString string

	// See: https://stackoverflow.com/questions/64272533/get-request-returns-403-status-code-parsing
	// needed for http://www.cnvc.org/trainers
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			MaxVersion: tls.VersionTLS12,
		},
	}
	client := &http.Client{Transport: tr}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return resString, fmt.Errorf("when fetching url, error in creating new request: %w", err)
	}
	req.Header.Set("User-Agent", s.UserAgent)
	req.Header.Set("Accept", "*/*")
	// req.Header.Set("Accept-Encoding", "identity")
	// req.Header.Set("Connection", "Keep-Alive")

	res, err := client.Do(req)
	if err != nil {
		return resString, fmt.Errorf("when fetching url, error in doing request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		return resString, fmt.Errorf("when fetching url, status code error: %d %s", res.StatusCode, res.Status)
	}
	bytes, err := io.ReadAll(res.Body)
	if err != nil {
		return resString, err
	}
	resString = string(bytes)
	log.Printf("StaticFetcher.Fetch() returning resString")
	return resString, nil
}

// The DynamicFetcher renders js
type DynamicFetcher struct {
	UserAgent        string
	WaitMilliseconds int
	allocContext     context.Context
	cancelAlloc      context.CancelFunc
}

func NewDynamicFetcher(ua string, ms int) *DynamicFetcher {
	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(1920, 1080), // init with a desktop view (sometimes pages look different on mobile, eg buttons are missing)
	)
	if ua != "" {
		opts = append(opts,
			chromedp.UserAgent(ua))
	}
	allocContext, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	d := &DynamicFetcher{
		UserAgent:        ua,
		WaitMilliseconds: ms,
		allocContext:     allocContext,
		cancelAlloc:      cancelAlloc,
	}
	if d.WaitMilliseconds == 0 {
		d.WaitMilliseconds = 2000 // default
	}
	return d
}

func (d *DynamicFetcher) Cancel() {
	d.cancelAlloc()
}

func (d *DynamicFetcher) Fetch(urlStr string, opts FetchOpts) (string, error) {
	logger := slog.With(slog.String("fetcher", "dynamic"), slog.String("url", urlStr))
	logger.Debug("fetching page", slog.String("user-agent", d.UserAgent))
	// start := time.Now()
	ctx, cancel := chromedp.NewContext(d.allocContext)
	// ctx, cancel := chromedp.NewContext(d.allocContext,
	// 	chromedp.WithLogf(log.Printf),
	// 	chromedp.WithDebugf(log.Printf),
	// 	chromedp.WithErrorf(log.Printf),
	// )
	defer cancel()
	var body string
	sleepTime := time.Duration(d.WaitMilliseconds) * time.Millisecond
	actions := []chromedp.Action{
		chromedp.Navigate(urlStr),
		chromedp.Sleep(sleepTime),
	}
	logger.Debug(fmt.Sprintf("appended chrome actions: Navigate, Sleep(%v)", sleepTime))
	for j, ia := range opts.Interaction {
		logger.Debug(fmt.Sprintf("processing interaction nr %d, type %s", j, ia.Type))
		delay := 500 * time.Millisecond // default is .5 seconds
		if ia.Delay > 0 {
			delay = time.Duration(ia.Delay) * time.Millisecond
		}
		if ia.Type == types.InteractionTypeClick {
			count := 1 // default is 1
			if ia.Count > 0 {
				count = ia.Count
			}
			for i := 0; i < count; i++ {
				// we only click the button if it exists. Do we really need this check here?
				actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
					var nodes []*cdp.Node
					if err := chromedp.Nodes(ia.Selector, &nodes, chromedp.AtLeast(0)).Do(ctx); err != nil {
						return err
					}
					if len(nodes) == 0 {
						return nil
					} // nothing to do
					logger.Debug(fmt.Sprintf("clicking on node with selector: %s", ia.Selector))
					return chromedp.MouseClickNode(nodes[0]).Do(ctx)
				}))
				actions = append(actions, chromedp.Sleep(delay))
				logger.Debug(fmt.Sprintf("appended chrome actions: ActionFunc (mouse click), Sleep(%v)", delay))
			}
		}
	}
	actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
		node, err := dom.GetDocument().Do(ctx)
		if err != nil {
			return err
		}
		body, err = dom.GetOuterHTML().WithNodeID(node.NodeID).Do(ctx)
		return err
	}))

	if config.Debug {
		u, _ := url.Parse(urlStr)
		var buf []byte
		r, err := utils.RandomString(u.Host)
		if err != nil {
			return "", err
		}
		filename := fmt.Sprintf("%s.png", r)
		actions = append(actions, chromedp.CaptureScreenshot(&buf))
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			logger.Debug(fmt.Sprintf("writing screenshot to file %s", filename))
			return os.WriteFile(filename, buf, 0644)
		}))
		logger.Debug("appended chrome actions: CaptureScreenshot, ActionFunc (save screenshot)")
	}

	// run task list
	err := chromedp.Run(ctx,
		actions...,
	)
	// elapsed := time.Since(start)
	// log.Printf("fetching %s took %s", url, elapsed)
	return body, err
}

// The FileFetcher fetches static page content
type FileFetcher struct {
}

func (s *FileFetcher) Fetch(url string, opts FetchOpts) (string, error) {
	fpath := strings.TrimPrefix(url, "file://")
	bs, err := ioutil.ReadFile(fpath)
	if err != nil {
		return "", fmt.Errorf("error reading file %q: %w", fpath, err)
	}
	return string(bs), nil
}
