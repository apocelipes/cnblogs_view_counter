package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
)

type PostInfo struct {
	Title     string
	Date      time.Time
	ViewCount uint64
}

var wg sync.WaitGroup

func calcViewer(data <-chan PostInfo, ret *atomic.Uint64) {
	defer wg.Done()
	var sum uint64
	for v := range data {
		sum += v.ViewCount
	}
	ret.Swap(sum)
}

func getNextPageURL(url *string, currentPageIndex *int) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		nodes := make([]*cdp.Node, 0)
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

func parseClassDay(nodes []*cdp.Node, data chan<- PostInfo) {
	for _, post := range nodes {
		titleNode := searchNodeByClass(post.Children, "postTitle")
		// .postTitle > a > span > TextNode
		title := titleNode.Children[0].Children[0].Children[0].NodeValue
		title = strings.TrimSpace(title)
		descNode := searchNodeByClass(post.Children, "postDesc")
		date, count := getPostMetaData(descNode)
		data <- PostInfo{
			Title:     title,
			Date:      date,
			ViewCount: count,
		}
	}
	wg.Done()
}

func parseClassPost(nodes []*cdp.Node, data chan<- PostInfo) {
	for _, post := range nodes {
		titleNode := post.Children[0]
		// .postTitle > a > span > TextNode
		title := titleNode.Children[0].Children[0].Children[0].NodeValue
		title = strings.TrimSpace(title)
		descNode := searchNodeByClass(post.Children, "postfoot")
		date, count := getPostMetaData(descNode)
		data <- PostInfo{
			Title:     title,
			Date:      date,
			ViewCount: count,
		}
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

func getAllPosts(data chan<- PostInfo) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		const timeLimit = 5 * time.Second
		nodes := make([]*cdp.Node, 0, 6)
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
			go parseClassDay(nodes, data)
		case PostPost:
			wg.Add(1)
			go parseClassPost(nodes, data)
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
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.Flag("disable-extensions", true),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)
	defer cc()
	// create chrome instance
	ctx, cancel := chromedp.NewContext(
		ctx,
		chromedp.WithLogf(log.Printf),
	)
	defer cancel()

	data := make(chan PostInfo, 10)
	var res atomic.Uint64
	wg.Add(1)
	go calcViewer(data, &res)

	url := fmt.Sprintf("https://www.cnblogs.com/%s/", *blogUserName)
	pageCounter := 1
	for url != "" {
		//TODO: progressbar
		fmt.Println("page:", pageCounter)
		err := chromedp.Run(ctx,
			chromedp.Navigate(url),
			chromedp.WaitReady(`.day *, .post *`, chromedp.ByQueryAll),
			getAllPosts(data),
			getNextPageURL(&url, &pageCounter),
		)
		if err != nil {
			log.Fatal(err)
		}
		time.Sleep(time.Duration(rand.Intn(3)+1) * time.Second)
	}
	close(data)
	wg.Wait()
	fmt.Printf("User: %s\ttotal view count: %d\n", *blogUserName, res.Load())
}
