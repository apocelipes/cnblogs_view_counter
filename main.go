package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
)

var (
	wg      sync.WaitGroup
	counter atomic.Uint64
)

func getNextPageURL(url *string, currentPageIndex *int) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		var nodes []*cdp.Node
		*url = ""
		if *currentPageIndex == 1 {
			var ok bool
			// timeout means there's no next-page button
			timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 2*time.Second)
			err := chromedp.AttributeValue("#nav_next_page a", "href", url, &ok, chromedp.ByQuery).Do(timeoutCtx)
			timeoutCancel()
			if err != nil {
				return err
			}
			*currentPageIndex += 1
			return nil
		}

		err := chromedp.Nodes("#homepage_top_pager div.pager a", &nodes, chromedp.ByQueryAll).Do(ctx)
		if err != nil {
			return err
		}

		if len(nodes) < 1 {
			return errors.New("can not find next-page")
		}
		node := nodes[len(nodes)-1]
		if node.Children[0].NodeValue == "下一页" {
			*url = node.AttributeValue("href")
		}
		*currentPageIndex += 1
		return nil
	}
}

var postTimePattern = regexp.MustCompile(`posted @ (\d{4}-\d{2}-\d{2}\s\d{2}:\d{2})`)
var countPattern = regexp.MustCompile(`阅读\(([0-9]+)\)`)

func parseClassDay(nodes []*cdp.Node) {
	for _, post := range nodes {
		descNode := searchNodeByClass(post.Children, "postDesc")
		_, count := getPostMetaData(descNode)
		counter.Add(count)
	}
	wg.Done()
}

func parseClassPost(nodes []*cdp.Node) {
	for _, post := range nodes {
		descNode := searchNodeByClass(post.Children, "postfoot")
		_, count := getPostMetaData(descNode)
		counter.Add(count)
	}
	wg.Done()
}

func getPostMetaData(descNode *cdp.Node) (time.Time, uint64) {
	// node > text
	timeStr := postTimePattern.FindStringSubmatch(descNode.Children[0].NodeValue)[1]
	date, _ := time.Parse("2006-01-02 15:04", timeStr)
	// node > .post-view-count
	viewCountNode := searchNodeByClass(descNode.Children, "post-view-count")
	countStr := countPattern.FindStringSubmatch(viewCountNode.Children[0].NodeValue)[1]
	count, _ := strconv.ParseUint(countStr, 10, 64)
	return date, count
}

type PostType uint8

const (
	PostDay PostType = iota
	PostPost
)

func getAllPosts() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		const timeLimit = 5 * time.Second
		var nodes []*cdp.Node
		blogPostType := PostDay
		timeoutCtx, timeoutCancel := context.WithTimeout(ctx, timeLimit)
		err := chromedp.Nodes(".day", &nodes, chromedp.ByQueryAll).Do(timeoutCtx)
		timeoutCancel()
		if errors.Is(err, context.DeadlineExceeded) {
			timeoutCtx, timeoutCancel := context.WithTimeout(ctx, timeLimit)
			err := chromedp.Nodes(".post", &nodes, chromedp.ByQueryAll).Do(timeoutCtx)
			timeoutCancel()
			if err != nil {
				return err
			}
			blogPostType = PostPost
		}
		if len(nodes) == 0 {
			return errors.New("no post")
		}

		switch blogPostType {
		case PostDay:
			wg.Add(1)
			go parseClassDay(nodes)
		case PostPost:
			wg.Add(1)
			go parseClassPost(nodes)
		}

		return nil
	}
}

func searchNodeByClass(arr []*cdp.Node, class string) (ret *cdp.Node) {
	for _, node := range arr {
		if c, ok := node.Attribute("class"); ok && (c == class) {
			ret = node
			return
		}
	}
	return
}

func main() {
	blogUserName := flag.String("user", "apocelipes", "cnblog user's name")
	flag.Parse()

	ctx, cc := chromedp.NewExecAllocator(context.Background(),
		chromedp.NoDefaultBrowserCheck,
		chromedp.NoFirstRun,
		chromedp.IgnoreCertErrors,
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.Flag("disable-extensions", true),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"),
	)
	defer cc()
	// create chrome instance
	ctx, cancel := chromedp.NewContext(
		ctx,
		chromedp.WithLogf(log.Printf),
	)
	defer cancel()

	const userPrompt = "User"
	const countPrompt = "Count of reading"
	const workerPrompt = "working on page"
	const padding = max(len(countPrompt), len(workerPrompt), len(userPrompt))

	url := fmt.Sprintf("https://www.cnblogs.com/%s/", *blogUserName)
	pageCounter := 1
	for url != "" {
		//TODO: progressbar
		fmt.Printf("%[1]*[2]s: %[3]d\n", padding, workerPrompt, pageCounter)
		resp, err := chromedp.RunResponse(ctx, chromedp.Navigate(url))
		if err != nil {
			log.Fatal(err)
		}
		if resp.Status != 200 {
			log.Fatalf("request failed with status code: %d", resp.Status)
		}
		err = chromedp.Run(ctx,
			chromedp.WaitReady(`.day *, .post *`, chromedp.ByQueryAll),
			getAllPosts(),
			getNextPageURL(&url, &pageCounter),
		)
		if err != nil {
			log.Fatal(err)
		}
		time.Sleep(time.Duration(rand.IntN(3)+1) * time.Second)
	}
	wg.Wait()

	fmt.Printf("\n%[1]*[2]s: %[3]s\n", padding, userPrompt, *blogUserName)
	fmt.Printf("%[1]*[2]s: %[3]d\n", padding, countPrompt, counter.Load())
}
