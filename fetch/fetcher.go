package fetch

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/chromedp"
	"github.com/findyourpaths/goskyr/config"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/gosimple/slug"
	"golang.org/x/net/publicsuffix"
)

// Interaction represents a simple user interaction with a webpage
type Interaction struct {
	Type     string `yaml:"type,omitempty"`
	Selector string `yaml:"selector,omitempty"`
	Count    int    `yaml:"count,omitempty"`
	Delay    int    `yaml:"delay,omitempty"`
}

const (
	InteractionTypeClick  = "click"
	InteractionTypeScroll = "scroll"
)

type FetchOpts struct {
	Interaction []*Interaction
}

// A Fetcher allows to fetch the content of a web page
type Fetcher interface {
	Fetch(url string, opts *FetchOpts) (*URLResponse, error)
}

type URLResponse struct {
	RequestedURL string
	ResolvedURL  string
	StatusCode   int
	ContentType  string
	Data         []byte
}

// The StaticFetcher fetches static page content
type StaticFetcher struct {
	UserAgent string
	Jar       *cookiejar.Jar
	client    *http.Client
}

func (s *StaticFetcher) SetTransport(tr http.RoundTripper) {
	s.client.Transport = tr
}

var htmlOutputDir = "/tmp/goskyr/scraper/fetchToDoc/"

func TrimURLScheme(u string) string {
	u = strings.TrimPrefix(u, "file://")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "www.")
	return u
}

func MakeURLStringSlug(u string) string {
	return slug.Make(TrimURLScheme(u))
}

func GQDocumentFromURLResponse(urlResp *URLResponse) (*Document, error) {
	// slog.Debug("Scraper.fetchToDoc(urlStr: %q, opts %#v)", urlStr, opts)
	// slog.Debug("in Scraper.fetchToDoc(), c.fetcher: %#v", c.fetcher)
	// fmt.Println(res)
	doc, err := NewDocumentFromString(string(urlResp.Data))
	if err != nil {
		return nil, err
	}
	doc.Url, err = url.Parse(urlResp.ResolvedURL)
	if err != nil {
		return nil, err
	}

	if !config.Debug {
		return doc, nil
	}

	// In debug mode we want to write all the htmls to files.
	htmlStr, err := goquery.OuterHtml(doc.Children())
	if err != nil {
		return nil, fmt.Errorf("failed to write html file: %v", err)
	}

	slog.Debug("writing html to file", "urlResp.ResolvedURL", urlResp.ResolvedURL)
	fpath, err := utils.WriteTempStringFile(filepath.Join(htmlOutputDir, slug.Make(urlResp.ResolvedURL)+".html"), htmlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to write html file: %v", err)
	}
	slog.Debug("wrote html to file", "fpath", fpath)

	return doc, nil
}

func NewStaticFetcher() *StaticFetcher {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		slog.Error("failed to set cookie jar", "err", err)
		return nil
	}

	// See: https://stackoverflow.com/questions/64272533/get-request-returns-403-status-code-parsing
	// needed for http://www.cnvc.org/trainers
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			MaxVersion: tls.VersionTLS12,
		},
	}

	return &StaticFetcher{
		client: &http.Client{
			Transport: tr,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// This function will be called before following any redirect.
				// Returning http.ErrUseLastResponse will cause the Client's Get/Post method
				// to return the most recent response (the one with the redirect),
				// without following the redirect.
				return http.ErrUseLastResponse
			},
			Jar: jar,
		},
	}
}

func (s *StaticFetcher) Fetch(u string, opts *FetchOpts) (*URLResponse, error) {
	// func GetFromURL(u string) (*URLResponse, error) { // , opts *FetchOpts
	// log.Printf("StaticFetcher.Fetch(url: %q, opts: %#v)", url, opts)
	// s.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36"
	// slog.Debug("StaticFetcher.Fetch()", "u", u)
	userAgent := "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/51.0.2704.103 Safari/537.36"
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36"

	// fmt.Printf("static fetching: %q\n", url)
	r := &URLResponse{RequestedURL: u}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return r, fmt.Errorf("error in creating new request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	// req.Header.Set("Accept-Encoding", "identity")
	// req.Header.Set("Connection", "Keep-Alive")

	resp, err := s.client.Do(req)
	if err != nil {
		// If CheckRedirect returns an error other than http.ErrUseLastResponse,
		// that error will be returned here.
		// If http.ErrUseLastResponse is returned by CheckRedirect, err will be nil here,
		// and you'll get the response that was trying to redirect.
		slog.Debug("in util.GetFromURL(), error making GET request", "err", err)
		// Note: Even with http.ErrUseLastResponse, if there's another network error
		// before the redirect is even attempted, it will be caught here.
		// However, a successful fetch of a redirecting response will NOT result in an error here
		// if http.ErrUseLastResponse is used.

		// To specifically check if the error is due to a redirect policy
		// when not using http.ErrUseLastResponse (e.g., if you returned a custom error):
		// if urlError, ok := err.(*url.Error); ok {
		//    // urlError.Err might be your custom error from CheckRedirect
		//    fmt.Println("Redirect error:", urlError.Err)
		// }
		return r, fmt.Errorf("in util.GetFromURL(), error making GET request: %w", err)
	}
	defer resp.Body.Close()

	r.StatusCode = resp.StatusCode
	r.ContentType = resp.Header.Get("content-type")

	resURLU, err := resp.Location()
	// slog.Debug("in Fetch()", "resURLU", resURLU)
	if err != nil {
		if err != http.ErrNoLocation {
			return r, fmt.Errorf("error in getting resolved URL: %w", err)
		}
		r.ResolvedURL = u
	} else {
		r.ResolvedURL = resURLU.String()
	}

	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		// slog.Debug("in Fetch()", "resp.StatusCode", resp.StatusCode, "r", r)
		return r, nil
	}

	// if resp.StatusCode != 200 {
	// 	return r, fmt.Errorf("unexpected status code: %d %s", resp.StatusCode, resp.Status)
	// }

	r.Data, err = io.ReadAll(resp.Body)
	if err != nil {
		return r, fmt.Errorf("error reading bytes: %w", err)
	}

	// log.Printf("StaticFetcher.Fetch() returning respString")
	slog.Debug("StaticFetcher.Fetch()", "r.ResolvedURL", r.ResolvedURL, "r.ContentType", r.ContentType)
	return r, nil
}

// The DynamicFetcher renders js
type DynamicFetcher struct {
	UserAgent        string
	WaitMilliseconds int
	allocContext     context.Context
	cancelAlloc      context.CancelFunc
}

var userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36"

func NewDynamicFetcher(ua string, ms int) *DynamicFetcher {
	if ua == "" {
		ua = userAgent
	}
	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(1920, 1080), // init with a desktop view (sometimes pages look different on mobile, eg buttons are missing)
		chromedp.UserAgent(ua),
	)

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

var pngOutputDir = "/tmp/goskyr/fetch/Fetch/"

func (d *DynamicFetcher) Fetch(urlStr string, opts *FetchOpts) (*URLResponse, error) {
	slog.Debug("DynamicFetcher.Fetch()", "urlStr", urlStr)
	if opts == nil {
		opts = &FetchOpts{}
	}

	// log.Printf("DynamicFetcher.Fetch(urlStr: %q, opts: %#v)", urlStr, opts)

	// slg := slog.With(slog.String("fetcher", "dynamic"), slog.String("url", urlStr))
	// slg.Info("fetching page", slog.String("user-agent", d.UserAgent))
	// start := time.Now()
	ctx, cancel := chromedp.NewContext(d.allocContext)
	// ctx, cancel := chromedp.NewContext(d.allocContext,
	// 	chromedp.WithLogf(log.Printf),
	// 	chromedp.WithDebugf(log.Printf),
	// 	chromedp.WithErrorf(log.Printf),
	// )
	defer cancel()

	if strings.HasPrefix(urlStr, "file://") && !strings.HasPrefix(urlStr, "file:///") {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("error getting working directory while absolutizing file url: %v", err)
		}
		urlStr = "file://" + wd + "/" + strings.TrimPrefix(urlStr, "file://")
	}
	// fmt.Printf("dynamic fetching: %q\n", urlStr)

	var resURL string
	sleepTime := time.Duration(d.WaitMilliseconds) * time.Millisecond
	actions := []chromedp.Action{
		chromedp.Navigate(urlStr),
		chromedp.Location(&resURL),
		chromedp.Sleep(sleepTime),
	}
	// slg.Debug(fmt.Sprintf("appended chrome actions: Navigate, Sleep(%v)", sleepTime))
	for j, ia := range opts.Interaction {
		slog.Debug("in DynamicFetcher.Fetch(), processing interaction", "j", j, "ia.Type", ia.Type)
		// slg.Debug(fmt.Sprintf("processing interaction nr %d, type %s", j, ia.Type))
		delay := 500 * time.Millisecond // default is .5 seconds
		if ia.Delay > 0 {
			delay = time.Duration(ia.Delay) * time.Millisecond
		}
		if ia.Type == InteractionTypeClick {
			count := 1 // default is 1
			if ia.Count > 0 {
				count = ia.Count
			}
			for range count {
				// we only click the button if it exists. Do we really need this check here?
				actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
					var nodes []*cdp.Node
					if err := chromedp.Nodes(ia.Selector, &nodes, chromedp.AtLeast(0)).Do(ctx); err != nil {
						return fmt.Errorf("error accessing nodes: %v", err)
					}
					if len(nodes) == 0 {
						return nil
					} // nothing to do
					slog.Debug("in DynamicFetcher.Fetch(), clicking on node with selector", "ia.Selector", ia.Selector)
					// slg.Debug(fmt.Sprintf("clicking on node with selector: %s", ia.Selector))
					return chromedp.MouseClickNode(nodes[0]).Do(ctx)
				}))
				actions = append(actions, chromedp.Sleep(delay))
				slog.Debug("in DynamicFetcher.Fetch(), appended chrome actions: ActionFunc (mouse click), Sleep", "delay", delay)
				// slg.Debug(fmt.Sprintf("appended chrome actions: ActionFunc (mouse click), Sleep(%v)", delay))
			}
		}
	}

	var body string
	actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
		node, err := dom.GetDocument().Do(ctx)
		if err != nil {
			return fmt.Errorf("error getting document: %v", err)
		}
		body, err = dom.GetOuterHTML().WithNodeID(node.NodeID).Do(ctx)
		if err != nil {
			return fmt.Errorf("error getting node: %v", err)
		}
		return nil
	}))

	if config.Debug || DoDebug {
		u, _ := url.Parse(urlStr)
		var buf []byte
		actions = append(actions, chromedp.CaptureScreenshot(&buf))
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			slog.Debug("in DynamicFetcher.Fetch(), writing screenshot to file")
			// slg.Debug(fmt.Sprintf("writing screenshot to file"))
			fpath, err := utils.WriteTempStringFile(filepath.Join(pngOutputDir, u.Host+".png"), string(buf))
			if err != nil {
				return err
			}
			slog.Debug("in DynamicFetcher.Fetch(), wrote screenshot to file", "fpath", fpath)
			// slg.Debug(fmt.Sprintf("wrote screenshot to file %s", fpath))
			return nil
		}))
		slog.Debug("in DynamicFetcher.Fetch(), appended chrome actions: CaptureScreenshot, ActionFunc (save screenshot)")
		// slg.Debug("appended chrome actions: CaptureScreenshot, ActionFunc (save screenshot)")
	}

	// run task list
	resp, err := chromedp.RunResponse(ctx, actions...)
	if err != nil {
		return nil, fmt.Errorf("error running chromedp for url %q: %v", err, urlStr)
	}

	if DoDebug && resURL != resp.URL {
		slog.Warn("DynamicFetcher.Fetch()", "resURL", resURL)
		slog.Warn("DynamicFetcher.Fetch()", "resp.URL", resp.URL)
		// slog.Warn("DynamicFetcher.Fetch()", "resp", resp)
	}

	r := &URLResponse{
		RequestedURL: urlStr,
		Data:         []byte(body),
		ResolvedURL:  resURL,
		StatusCode:   int(resp.Status),
		ContentType:  resp.Headers["content-type"].(string),
	}

	// r.Data = body
	// r.Data, err = io.ReadAll(resp.Body)
	// if err != nil {
	// 	return r, fmt.Errorf("error reading bytes: %w", err)
	// }

	slog.Debug("DynamicFetcher.Fetch()", "r.ResolvedURL", r.ResolvedURL, "r.ContentType", r.ContentType, "len(r.Data)", len(r.Data))
	return r, nil
}

// The FileFetcher fetches static page content
type FileFetcher struct {
}

func (s *FileFetcher) Fetch(url string, opts *FetchOpts) (string, error) {
	// log.Printf("FileFetcher.Fetch(url: %q, opts: %#v)", url, opts)
	fpath := strings.TrimPrefix(url, "file://")
	bs, err := ioutil.ReadFile(fpath)
	if err != nil {
		return "", fmt.Errorf("error reading file %q: %w", fpath, err)
	}
	return string(bs), nil
}
