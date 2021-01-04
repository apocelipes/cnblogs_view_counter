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
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	blogUserName := flag.String("user", "apocelipes", "cnblog user's name")
	flag.Parse()

	c, cc := chromedp.NewExecAllocator(context.Background(),
		chromedp.NoDefaultBrowserCheck,
		chromedp.NoFirstRun,
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.Flag("disable-extensions", true),
		chromedp.UserAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/87.0.4280.66 Safari/537.36"),
	)
	defer cc()
	// create chrome instance
	ctx, cancel := chromedp.NewContext(
		c,
		chromedp.WithLogf(log.Printf),
	)
	defer cancel()

	data := make(chan string, 10)
	resChan := make(chan int)
	go func() {
		pattern := regexp.MustCompile(`阅读\(([0-9]+)\)`)
		res := 0
		for v := range data {
			read := pattern.FindStringSubmatch(v)[1]
			readCounter, _ := strconv.Atoi(read)
			res += readCounter
		}
		resChan <- res
	}()

	url := fmt.Sprintf("https://www.cnblogs.com/%s/", *blogUserName)
	var nodes []*cdp.Node
	pageCounter := 1
	for url != "" {
		//TODO: progressbar
		fmt.Println("page:", pageCounter)
		err := chromedp.Run(ctx,
			chromedp.Navigate(url),
			chromedp.WaitReady(`.post-view-count`, chromedp.ByQueryAll),
			chromedp.Nodes(".post-view-count", &nodes, chromedp.ByQueryAll),
			chromedp.ActionFunc(func(ctx context.Context) error {
				for _, v := range nodes {
					//log.Println("node:", v.Children[0].NodeValue)
					data <- v.Children[0].NodeValue
				}
				return nil
			}),
			chromedp.ActionFunc(func(ctx context.Context) error {
				url = ""
				if pageCounter == 1 {
					var ok bool
					// timeout means there's no next-page button
					timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 2*time.Second)
					defer timeoutCancel()
					err := chromedp.AttributeValue("#nav_next_page a", "href", &url, &ok, chromedp.ByQuery).Do(timeoutCtx)
					if err != nil {
						return err
					}
					pageCounter++
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
					url = node.AttributeValue("href")
				}
				pageCounter++
				return nil
			}),
		)
		if err != nil {
			log.Fatal(err)
		}
		time.Sleep(time.Duration(rand.Intn(3)+1) * time.Second)
	}
	close(data)
	fmt.Println("total:", <-resChan)
}
