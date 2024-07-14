package vice

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/earthboundkid/deque/v2"
	"github.com/hashicorp/go-set"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/html"
)

var ErrorNoAttribute = errors.New("Could not find attribute")

func GetNodeAttribute(node *html.Node, name string, URL string) (string, error) {
	var result *string
	for _, a := range node.Attr {
		if strings.EqualFold(a.Key, name) {
			if result != nil {
				log.Warnf("Multiple \"%s\" attributes in \"%s\"", name, URL)
			}
			result = &a.Val
		}
	}
	if result == nil {
		return "", ErrorNoAttribute
	}
	return *result, nil
}

func analyze(doc *goquery.Document) {
}

func flattenPath(path string) string {
	var newPathParts []string
	pathParts := strings.Split(path, "/")
	for _, part := range pathParts {
		if part == ".." {
			if len(newPathParts) > 0 {
				newPathParts = newPathParts[:len(newPathParts)-1]
			}
		} else {
			newPathParts = append(newPathParts, part)
		}
	}
	return strings.Join(newPathParts, "/")
}

func makeAbsolute(URL url.URL, directoryURL url.URL, originPath string) *url.URL {
	if URL.Scheme == "" {
		URL.Scheme = directoryURL.Scheme
		if URL.Host == "" {
			URL.Host = directoryURL.Host
			URL.OmitHost = false
			URL.User = directoryURL.User
			if URL.Path == "" {
				// Special case when URL is "#smth" or "".
				URL.Path = originPath
			}
		}
	}
	if URL.Path != "" && !strings.HasPrefix(URL.Path, "/") {
		URL.Path = directoryURL.Path + URL.Path
	}
	return &URL
}

type URLWithAnchor struct {
	URL    string
	Anchor string
}

type LinkWithReferer struct {
	URL        string
	RefererURL string
}

func processLink(
	queue *deque.Deque[LinkWithReferer],
	external *set.Set[string],
	requiredAnchors *map[URLWithAnchor][]string,
	directoryParts *url.URL,
	rootParts *url.URL,
	originPath string,
	URL string,
	link string,
) {
	linkParts, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		log.Errorf("Can't parse URL \"%s\" in \"%s\": %s", link, URL, err.Error())
	} else {
		linkParts = makeAbsolute(*linkParts, *directoryParts, originPath)
		linkParts.Path = flattenPath(linkParts.Path)

		var fragment string
		fragment, linkParts.Fragment = linkParts.Fragment, ""
		link := linkParts.String()
		if fragment != "" {
			URLWithAnchor := URLWithAnchor{link, fragment}
			_, ok := (*requiredAnchors)[URLWithAnchor]
			if !ok {
				(*requiredAnchors)[URLWithAnchor] = []string{}
			}
			(*requiredAnchors)[URLWithAnchor] = append((*requiredAnchors)[URLWithAnchor], URL)
		}

		if linkParts.Host != rootParts.Host || !strings.HasPrefix(linkParts.Path, rootParts.Path) || linkParts.Path != rootParts.Path && !strings.HasSuffix(rootParts.Path, "/") && rootParts.Path != "" {
			external.Insert(link)
		}
		queue.PushBack(LinkWithReferer{link, URL})
	}
}

func Check(rootURL string) {
	visited := set.New[string](10)
	notFound := set.New[string](10)
	external := set.New[string](10)
	queue := deque.Deque[LinkWithReferer]{}
	queue.PushBack(LinkWithReferer{rootURL, "<user request>"})
	pages := 0

	availableAnchors := set.New[URLWithAnchor](10)
	requiredAnchors := map[URLWithAnchor][]string{}

	rootParts, err := url.Parse(rootURL)
	if err != nil {
		log.Errorf("Can't parse root URL \"%s\": %s", rootURL, err.Error())
		return
	}

	for queue.Len() > 0 {
		linkWithReferer, _ := queue.RemoveFront()
		URL := linkWithReferer.URL
		if visited.Contains(URL) {
			continue
		}
		visited.Insert(URL)
		refererURL := linkWithReferer.RefererURL

		log.Debugf("Scanning \"%s\"", URL)
		response, err := http.Head(URL)
		if err != nil {
			notFound.Insert(URL)
			log.Errorf("Can't get \"%s\" referenced from \"%s\": %s", URL, refererURL, err.Error())
			continue
		}
		if response.StatusCode != http.StatusOK {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			notFound.Insert(URL)
			log.Errorf("Can't get \"%s\" referenced from \"%s\": %s", URL, refererURL, response.Status)
			continue
		}

		updatedURL := response.Request.URL.String()
		if URL != updatedURL {
			if URL == rootURL {
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				Check(updatedURL)
				return
			}
			if external.Contains(URL) {
				external.Insert(updatedURL)
			}
			queue.PushFront(LinkWithReferer{updatedURL, refererURL})
			log.Debugf("\"%s\" -> \"%s\"", URL, updatedURL)
		}

		contentType := response.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			notFound.Insert(URL)
			log.Errorf("Can't parse Content-Type \"%s\", \"%s\": %s", mediaType, URL, err.Error())
			continue
		}
		if mediaType != "text/html" && mediaType != "application/xhtml+xml" {
			continue
		}
		pages += 1

		response, err = http.Get(updatedURL)
		if err != nil {
			notFound.Insert(URL)
			log.Errorf("Can't get \"%s\" referenced from \"%s\": %s", URL, refererURL, err.Error())
			continue
		}
		if response.StatusCode != http.StatusOK {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			notFound.Insert(URL)
			log.Errorf("Can't get \"%s\" referenced from \"%s\": %s", URL, refererURL, response.Status)
			continue
		}

		doc, err := goquery.NewDocumentFromReader(response.Body)
		io.Copy(io.Discard, response.Body)
		response.Body.Close()

		if err != nil {
			log.Errorf("Can't parse \"%s\": %s", URL, err.Error())
		} else {
			var baseURL string
			if !external.Contains(URL) {
				bases := doc.Find("base").Nodes
				if len(bases) > 1 {
					log.Errorf("More than one \"base\" tag in \"%s\"", URL)
				}
				if len(bases) > 0 {
					baseURL, err = GetNodeAttribute(bases[0], "href", URL)
					if err != nil {
						log.Errorf("Can't get \"base\" href in \"%s\": %s", URL, err.Error())
					}
				}
			}

			directoryParts, err := url.Parse(updatedURL)
			if err != nil {
				log.Errorf("Can't parse URL \"%s\": %s", URL, err.Error())
			}
			originPath := directoryParts.Path
			directoryParts.Path, _ = filepath.Split(directoryParts.Path)

			if baseURL != "" {
				baseParts, err := url.Parse(baseURL)
				if err != nil {
					log.Errorf("Can't parse base URL \"%s\": %s", URL, err.Error())
				}
				directoryParts = makeAbsolute(*baseParts, *directoryParts, originPath)
				directoryParts.Path, _ = filepath.Split(directoryParts.Path)
			}

			for _, element := range doc.Find("a").Nodes {
				if !external.Contains(URL) {
					href, err := GetNodeAttribute(element, "href", URL)
					if err != nil {
						log.Warnf("Can't get \"a\" href in \"%s\": %s", URL, err.Error())
					} else {
						processLink(&queue, external, &requiredAnchors, directoryParts, rootParts, originPath, URL, href)
					}
				}

				name, err := GetNodeAttribute(element, "name", URL)
				if err == nil {
					name = strings.TrimSpace(name)
					availableAnchors.Insert(URLWithAnchor{URL, name})
				}
			}

			if !external.Contains(URL) {
				for _, element := range doc.Find("img").Nodes {
					src, err := GetNodeAttribute(element, "src", URL)
					if err != nil {
						log.Errorf("Can't get \"img\" src in \"%s\": %s", URL, err.Error())
					} else {
						processLink(&queue, external, &requiredAnchors, directoryParts, rootParts, originPath, URL, src)
					}
				}
			}

			for _, element := range doc.Find("*").Nodes {
				ID, err := GetNodeAttribute(element, "id", URL)
				if err == nil {
					ID = strings.TrimSpace(ID)
					availableAnchors.Insert(URLWithAnchor{URL, ID})
				}
			}

			analyze(doc)
		}
	}

	missingAnchors := []URLWithAnchor{}
	for URLWithAnchor := range requiredAnchors {
		if !availableAnchors.Contains(URLWithAnchor) && !notFound.Contains(URLWithAnchor.URL) {
			missingAnchors = append(missingAnchors, URLWithAnchor)
		}
	}
	sort.Slice(missingAnchors, func(i, j int) bool {
		return missingAnchors[i].URL < missingAnchors[j].URL || missingAnchors[i].URL == missingAnchors[j].URL && missingAnchors[i].Anchor < missingAnchors[j].Anchor
	})
	for _, URLWithAnchor := range missingAnchors {
		log.Errorf("Missing anchor \"%s#%s\" referenced from \"%s\"", URLWithAnchor.URL, URLWithAnchor.Anchor, strings.Join(requiredAnchors[URLWithAnchor], "\", \""))
	}

	log.Infof("Total pages: %d", pages)
	log.Infof("Total links: %d", visited.Size())
}
