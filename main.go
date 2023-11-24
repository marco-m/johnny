package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

// seems this is the max accepted value by GitHub for pagination.
const kPageSize = 100

type Args struct {
	//
	StalePRs    *StalePRsCmd    `arg:"subcommand:stale-prs" help:"list the open PRs whose closing issues are already all closed."`
	MultiplePRs *MultiplePRsCmd `arg:"subcommand:multiple-prs" help:"list the open issues with multiple open closing PRs."`
}

type StalePRsCmd struct {
	Owner string `help:"repository owner" arg:"required"`
	Name  string `help:"repository name" arg:"required"`
	Max   int    `help:"maximum number of PRs to process, rounded to the page size (default: all)"`
}

type MultiplePRsCmd struct {
	Owner string `help:"repository owner" arg:"required"`
	Name  string `help:"repository name" arg:"required"`
	Max   int    `help:"maximum number of PRs to process, rounded to the page size (default: all)"`
}

func main() {
	if err := run(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run() error {
	var args Args
	argParser := arg.MustParse(&args)
	if argParser.Subcommand() == nil {
		argParser.Fail("missing subcommand")
	}

	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		return fmt.Errorf("missing environment variable GITHUB_TOKEN")
	}

	tokenSource := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	httpClient := oauth2.NewClient(context.Background(), tokenSource)
	ghGQl := githubv4.NewClient(httpClient)
	ctx := context.Background()

	switch {
	case args.StalePRs != nil:
		return CmdStalePRs(ctx, ghGQl, args.StalePRs)
	case args.MultiplePRs != nil:
		return CmdMultiplePRs(ctx, ghGQl, args.MultiplePRs)
	default:
		return fmt.Errorf("internal error: unwired subcommand: %s",
			argParser.Subcommand())
	}
}

// Issue is either a GitHub issue or a GitHub pull request.
// (Go ask GitHub why).
// https://docs.github.com/en/graphql/reference/objects#issue
type Issue struct {
	Title     string
	Number    int // For example "#123" In GitHub comments.
	Closed    bool
	Url       string
	CreatedAt time.Time
	ClosedAt  time.Time
}

// RepoQuery is a query to obtain all PRs of a repository with their closing
// issues. Meant to be filled by function [repoPaginationLoop].
// FIXME super ugly names of variables hardcoded in the struct tags, super
// error-prone also...
type RepoQuery struct {
	// https://docs.github.com/en/graphql/reference/objects#repository
	Repository struct {
		Description  string
		Url          string
		PullRequests struct {
			Nodes []struct {
				Issue // This is a PR.
				// NOTA BENE We make the assumption that we can get all
				// the closing issues of a PR without pagination!
				ClosingIssuesReferences struct {
					Nodes []Issue // These are real issues.
				} `graphql:"closingIssuesReferences(last: 100)"`
			}
			PageInfo struct {
				EndCursor   githubv4.String
				HasNextPage bool
			}
		} `graphql:"pullRequests(first: $kPageSize, after: $prCursor, states: OPEN)"`
	} `graphql:"repository(owner: $repoOwner, name: $repoName)"`
}

func CmdStalePRs(ctx context.Context, ghGQl *githubv4.Client,
	args *StalePRsCmd) error {
	type stalePR struct {
		pr           Issue
		closedIssues []Issue
	}
	var stalePRs []stalePR
	var query RepoQuery

	// FIXME maybe remove error from f signature
	handler := func() error {
		for _, pr := range query.Repository.PullRequests.Nodes {
			if len(pr.ClosingIssuesReferences.Nodes) == 0 {
				continue
			}
			// First, collect from the closing issues the ones that are
			// already closed.
			var closed []Issue
			for _, closing := range pr.ClosingIssuesReferences.Nodes {
				if closing.Closed {
					closed = append(closed, closing)
				}
			}
			// Second, collect the PR only if _all_ the closing issues are
			// already closed.
			if len(closed) == len(pr.ClosingIssuesReferences.Nodes) {
				stalePRs = append(stalePRs, stalePR{
					pr:           pr.Issue,
					closedIssues: closed,
				})
			}
		}
		return nil
	}

	if err := repoPaginationLoop(ctx, ghGQl, args.Owner, args.Name,
		args.Max, &query, handler); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Repo:", query.Repository.Url)
	fmt.Println("List of open PRs whose closing issues are already all closed")
	fmt.Println()

	for i, stale := range stalePRs {
		fmt.Printf("%d. PR [#%d](%s) %s\n",
			i+1, stale.pr.Number, stale.pr.Url, stale.pr.Title)
		for _, issue := range stale.closedIssues {
			fmt.Printf("  - issue [#%d](%s) %s\n",
				issue.Number, issue.Url, issue.Title)
		}
		fmt.Println()
	}
	return nil
}

func CmdMultiplePRs(ctx context.Context, ghGQl *githubv4.Client,
	args *MultiplePRsCmd) error {
	// Design point.
	// We would like to list all issues, and for each issue list all the PRs
	// that will close this issue.
	// Although an issue has a list of `trackedIssues`, it seems that it is not
	// possible to discriminate between PRs and real issues.
	// Note also that "tracked" doesn't mean "closing"! It can mean simply
	// "mentioned".
	// So, we must fall back to reconstruct indirectly, starting from the list
	// of PRs and collecting the issues as keys in a map issue to PRs.

	// real issue to list of PRs
	is2pr := make(map[Issue][]Issue)
	var query RepoQuery

	handler := func() error {
		for _, pr := range query.Repository.PullRequests.Nodes {
			if len(pr.ClosingIssuesReferences.Nodes) == 0 {
				continue
			}
			for _, closing := range pr.ClosingIssuesReferences.Nodes {
				if !closing.Closed {
					if is2pr[closing] == nil {
						is2pr[closing] = []Issue{}
					}
					is2pr[closing] = append(is2pr[closing], pr.Issue)
				}
			}
		}
		return nil
	}

	if err := repoPaginationLoop(ctx, ghGQl, args.Owner, args.Name,
		args.Max, &query, handler); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Repo:", query.Repository.Url)
	fmt.Println("List of open issues with multiple open closing PRs.")
	fmt.Println()

	// Ensure stable ordering of the map keys.
	issues := make([]Issue, len(is2pr))
	for k := range is2pr {
		issues = append(issues, k)
	}
	slices.SortFunc(issues, func(a, b Issue) int {
		return cmp.Compare(a.Number, b.Number)
	})

	count := 0
	for _, issue := range issues {
		prs := is2pr[issue]
		if len(prs) < 2 {
			continue
		}
		count++
		fmt.Printf("%d. Issue [#%d](%s) %s\n",
			count, issue.Number, issue.Url, issue.Title)
		for _, pr := range prs {
			fmt.Printf("  - PR [#%d](%s) %s\n",
				pr.Number, pr.Url, pr.Title)
		}
		fmt.Println()
	}
	return nil
}

func repoPaginationLoop(ctx context.Context, ghGQl *githubv4.Client,
	owner, name string, maxPRs int, query *RepoQuery,
	handler func() error) error {

	variables := map[string]interface{}{
		"repoOwner": githubv4.String(owner),
		"repoName":  githubv4.String(name),
		"kPageSize": githubv4.Int(kPageSize), // Pagination
		"prCursor":  (*githubv4.String)(nil), // Pagination
	}

	// Pagination loop.
	// We assume that we can hold everything in memory.
	totalPRs := 0
	fmt.Printf("paging:")
	for {
		if err := ghGQl.Query(ctx, &query, variables); err != nil {
			return err
		}
		totalPRs += len(query.Repository.PullRequests.Nodes)
		fmt.Printf(" %d", totalPRs)

		if err := handler(); err != nil {
			return err
		}

		if maxPRs > 0 && totalPRs > maxPRs {
			fmt.Println()
			fmt.Println("hit max ", maxPRs)
			break
		}
		if !query.Repository.PullRequests.PageInfo.HasNextPage {
			break
		}
		// Update pagination cursor.
		variables["prCursor"] =
			githubv4.NewString(query.Repository.PullRequests.PageInfo.EndCursor)
	}
	return nil
}
