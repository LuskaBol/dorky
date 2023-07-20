package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/google/go-github/v38/github"
	"github.com/xanzy/go-gitlab"
	"golang.org/x/oauth2"
	"golang.org/x/time/rate"
)

type config struct {
	orgFlag      bool
	repoFlag     bool
	userFlag     bool
	maxFlag      int
	cleanFlag    bool
	ghOnlyFlag   bool
	glOnlyFlag   bool
	simpleFlag   bool
	verboseFlag  bool
}

var (
	flags       = config{}
	urlRegexp   = regexp.MustCompile(`^https?://(?:www\.)?([^/]+)`)
	spaceRegexp = regexp.MustCompile(`\s+`)
)

func init() {
	flag.BoolVar(&flags.orgFlag, "o", false, "search for organization names")
	flag.BoolVar(&flags.repoFlag, "r", false, "search for repository names")
	flag.BoolVar(&flags.userFlag, "u", false, "search for username matches")
	flag.IntVar(&flags.maxFlag, "max", 10, "maximum search results per category")
	flag.BoolVar(&flags.cleanFlag, "c", false, "clean input URLs")
	flag.BoolVar(&flags.ghOnlyFlag, "gh", false, "search only GitHub")
	flag.BoolVar(&flags.glOnlyFlag, "gl", false, "search only GitLab")
	flag.BoolVar(&flags.simpleFlag, "s", false, "simple output style for piping to another tool")
	flag.BoolVar(&flags.verboseFlag, "v", false, "enable verbose mode")
}

func main() {
	flag.Parse()
	validateFlags(flags)

	verbosePrint("Reading and cleaning words...\n")
	words := readAndCleanWords(flags, flag.Args())
	verbosePrint("Words cleaned.\n")

	verbosePrint("Searching platforms...\n")
	searchPlatforms(words, flags)
	verbosePrint("Platform search completed.\n")
}

func validateFlags(cfg config) {
	if !(cfg.orgFlag || cfg.repoFlag || cfg.userFlag) {
		fmt.Println("At least one search flag (-o, -r, or -u) must be specified")
		os.Exit(1)
	}
	verbosePrint("Flags validated.\n")
}

func verbosePrint(format string, a ...interface{}) {
	if flags.verboseFlag {
		fmt.Printf(format, a...)
	}
}

func readAndCleanWords(cfg config, args []string) map[string]struct{} {
	words := make(map[string]struct{})

	if len(args) > 0 {
		for _, word := range args {
			processWord(word, words, cfg)
		}
	} else {
		scanner := bufio.NewScanner(os.Stdin)

		for scanner.Scan() {
			word := strings.TrimSpace(scanner.Text())
			processWord(word, words, cfg)
		}
		checkScannerError(scanner)
	}

	return words
}

func processWord(word string, words map[string]struct{}, cfg config) {
	if cfg.cleanFlag {
		word = cleanWord(word)
	}

	addWordToMap(words, word)
	word = removeWhitespace(word)
	wordLines := strings.Split(word, "\n")

	for _, w := range wordLines {
		addWordToMap(words, w)
	}
}

func addWordToMap(words map[string]struct{}, word string) {
	if _, exists := words[word]; !exists {
		words[word] = struct{}{}
	}
}

func checkScannerError(scanner *bufio.Scanner) {
	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading stdin: %s\n", err)
		os.Exit(1)
	}
}

func searchPlatforms(words map[string]struct{}, cfg config) {
	ghClient, ghErr := createGitHubClient()
	glClient, glErr := createGitLabClient()

	if ghErr != nil {
		fmt.Printf("Error creating GitHub client: %s\n", ghErr)
	}

	if glErr != nil {
		fmt.Printf("Error creating GitLab client: %s\n", glErr)
	}

	for word := range words {
		if !cfg.glOnlyFlag && ghErr == nil {
			verbosePrint("Searching GitHub for word: %s\n", word)
			searchGitHub(ghClient, word, cfg)
		}

		if !cfg.ghOnlyFlag && glErr == nil {
			verbosePrint("Searching GitLab for word: %s\n", word)
			searchGitLab(glClient, word, cfg)
		}
	}
}

func cleanWord(word string) string {
	match := urlRegexp.FindStringSubmatch(word)
	if len(match) > 1 {
		return match[1]
	}
	return word
}

func removeWhitespace(word string) string {
	removedSpaces := spaceRegexp.ReplaceAllString(word, "")
	withHyphens := spaceRegexp.ReplaceAllString(word, "-")
	return removedSpaces + "\n" + withHyphens
}

func searchGitHub(client *github.Client, query string, cfg config) {
	if client == nil {
		return
	}

	if cfg.orgFlag {
		searchGitHubOrganizations(client, query, cfg.maxFlag)
	}

	if cfg.repoFlag {
		searchGitHubRepositories(client, query, cfg.maxFlag)
	}

	if cfg.userFlag {
		searchGitHubUsers(client, query, cfg.maxFlag)
	}
}

func searchGitLab(client *gitlab.Client, query string, cfg config) {
	if client == nil {
		return
	}

	if cfg.orgFlag || cfg.userFlag {
		searchGitLabGroupsAndUsers(client, query, cfg.maxFlag)
	}

	if cfg.repoFlag {
		searchGitLabProjects(client, query, cfg.maxFlag)
	}
}

func searchGitHubOrganizations(client *github.Client, query string, maxResults int) {
	ctx := context.Background()

	opt := &github.SearchOptions{ListOptions: github.ListOptions{PerPage: maxResults}}
	results, _, err := client.Search.Users(ctx, "type:org "+query, opt)
	if err != nil {
		fmt.Printf("Error searching organizations: %s\n", err)
		return
	}

	orgLogins := make([]string, len(results.Users))
	for i, org := range results.Users {
		orgLogins[i] = *org.Login
	}

	printResults(fmt.Sprintf("GitHub organizations matching '%s'", query), orgLogins)
	
	// Save the content of orgLogins to a file called "organizations.txt"
	f, err := os.Create("github_organizations.txt")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()
	for _, org := range orgLogins {
		f.WriteString(org + "\n")
	}
}

func searchGitHubRepositories(client *github.Client, query string, maxResults int) {
	ctx := context.Background()

	opt := &github.SearchOptions{ListOptions: github.ListOptions{PerPage: maxResults}}
	results, _, err := client.Search.Repositories(ctx, query, opt)
	if err != nil {
		fmt.Printf("Error searching repositories: %s\n", err)
		return
	}

	repoNames := make([]string, len(results.Repositories))
	for i, repo := range results.Repositories {
		repoNames[i] = *repo.FullName
	}

	printResults(fmt.Sprintf("GitHub repositories matching '%s'", query), repoNames)

	// Save the content of repoNames to a file called "repositories.txt"
	f, err := os.Create("github_repositories.txt")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()
	for _, repo := range repoNames {
		f.WriteString(repo + "\n")
	}
}

func searchGitHubUsers(client *github.Client, query string, maxResults int) {
	ctx := context.Background()

	opt := &github.SearchOptions{ListOptions: github.ListOptions{PerPage: maxResults}}
	results, _, err := client.Search.Users(ctx, "type:user "+query, opt)
	if err != nil {
		fmt.Printf("Error searching users: %s\n", err)
		return
	}

	userLogins := make([]string, len(results.Users))
	for i, user := range results.Users {
		userLogins[i] = *user.Login
	}

	printResults(fmt.Sprintf("GitHub users matching '%s'", query), userLogins)

	// Save the content of userLogins to a file called "users.txt"
	f, err := os.Create("github_users.txt")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()
	for _, user := range userLogins {
		f.WriteString(user + "\n")
	}
}

func createGitHubClient() (*github.Client, error) {
	ctx := context.Background()
	token := os.Getenv("GITHUB_ACCESS_TOKEN")
	if token == "" {
		return nil, errors.New("GITHUB_ACCESS_TOKEN environment variable is not set")
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	tc.Transport = &rateLimitedTransport{
		transport: tc.Transport,
		limiter:   rate.NewLimiter(rate.Every(10), 10),
	}

	client := github.NewClient(tc)

	return client, nil
}

type rateLimitedTransport struct {
	transport http.RoundTripper
	limiter   *rate.Limiter
}

func (t *rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.limiter.Wait(context.Background()); err != nil {
		return nil, err
	}

	return t.transport.RoundTrip(req)
}

func searchGitLabGroupsAndUsers(client *gitlab.Client, query string, maxResults int) {
	opt := &gitlab.ListGroupsOptions{Search: gitlab.String(query), ListOptions: gitlab.ListOptions{PerPage: maxResults}}
	groups, _, err := client.Groups.ListGroups(opt)
	if err != nil {
		fmt.Printf("Error searching GitLab groups: %s\n", err)
		return
	}

	if flags.orgFlag {
		groupFullPaths := make([]string, len(groups))
		for i, group := range groups {
			groupFullPaths[i] = group.FullPath
		}

		printResults(fmt.Sprintf("GitLab groups matching '%s'", query), groupFullPaths)

		// Save the content of groupFullPaths to a file called "groups.txt"
		f, err := os.Create("gitlab_groups.txt")
		if err != nil {
			fmt.Println(err)
			return
		}
		defer f.Close()
		for _, group := range groupFullPaths {
			f.WriteString(group + "\n")
		}
	}

	users, _, err := client.Users.ListUsers(&gitlab.ListUsersOptions{Search: gitlab.String(query), ListOptions: gitlab.ListOptions{PerPage: maxResults}})
	if err != nil {
		fmt.Printf("Error searching GitLab users: %s\n", err)
		return
	}

	if flags.userFlag {
		userUsernames := make([]string, len(users))
		for i, user := range users {
			userUsernames[i] = user.Username
		}

		printResults(fmt.Sprintf("GitLab users matching '%s'", query), userUsernames)

		// Save the content of userUsernames to a file called "users.txt"
		f, err := os.Create("gitlab_users.txt")
		if err != nil {
			fmt.Println(err)
			return
		}
		defer f.Close()
		for _, user := range userUsernames {
			f.WriteString(user + "\n")
		}
	}
}

func searchGitLabProjects(client *gitlab.Client, query string, maxResults int) {
	opt := &gitlab.ListProjectsOptions{Search: gitlab.String(query), ListOptions: gitlab.ListOptions{PerPage: maxResults}}
	projects, _, err := client.Projects.ListProjects(opt)
	if err != nil {
		fmt.Printf("Error searching GitLab projects: %s\n", err)
		return
	}

	projectFullPaths := make([]string, len(projects))
	for i, project := range projects {
		projectFullPaths[i] = project.PathWithNamespace
	}

	printResults(fmt.Sprintf("GitLab projects matching '%s'", query), projectFullPaths)

	// Save the content of projectFullPaths to a file called "projects.txt"
	f, err := os.Create("gitlab_projects.txt")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()
	for _, project := range projectFullPaths {
		f.WriteString(project + "\n")
	}
}

func createGitLabClient() (*gitlab.Client, error) {
	token := os.Getenv("GITLAB_ACCESS_TOKEN")
	if token == "" {
		return nil, errors.New("GITLAB_ACCESS_TOKEN environment variable is not set")
	}

	client, err := gitlab.NewClient(token)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func printResults(header string, results []string) {
	if flags.simpleFlag {
		for _, result := range results {
			fmt.Println(result)
		}
	} else {
		fmt.Printf("\n%s:\n", header)
		for _, result := range results {
			fmt.Printf("- %s\n", result)
		}
	}
}