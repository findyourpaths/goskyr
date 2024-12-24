package generate

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"

	"github.com/agnivade/levenshtein"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"golang.org/x/net/html"
)

// A node is our representation of a node in an html tree
type node struct {
	tagName       string
	classes       []string
	pseudoClasses []string
}

var nodeStringsCache map[*node]string

func (n node) string() string {
	r := n.tagName
	for _, cl := range n.classes {
		// https://www.itsupportguides.com/knowledge-base/website-tips/css-colon-in-id/
		cl = strings.ReplaceAll(cl, ":", "\\:")
		cl = strings.ReplaceAll(cl, ">", "\\>")
		cl = strings.ReplaceAll(cl, "@", "\\@")
		// https://stackoverflow.com/questions/45293534/css-class-starting-with-number-is-not-getting-applied
		if unicode.IsDigit(rune(cl[0])) {
			cl = fmt.Sprintf(`\3%s`, cl)
		}
		r += fmt.Sprintf(".%s", cl)
	}
	if len(n.pseudoClasses) > 0 {
		r += fmt.Sprintf(":%s", strings.Join(n.pseudoClasses, ":"))
	}
	return r
}

func (n node) equals(n2 node) bool {
	if n.tagName == n2.tagName {
		if utils.SliceEquals(n.classes, n2.classes) {
			if utils.SliceEquals(n.pseudoClasses, n2.pseudoClasses) {
				return true
			}
		}
	}
	return false
}

// A path is a list of nodes starting from the root node and going down
// the html tree to a specific node
type path []node

func (p path) last() *node {
	return &p[len(p)-1]
}

func (p path) memoString(pathStringsCache map[*node]string) string {
	if len(p) == 0 {
		return ""
	}

	last := p.last()
	if str := pathStringsCache[last]; str != "" {
		return str
	}

	str := last.string()
	if prefix := p[0 : len(p)-1].memoString(pathStringsCache); prefix != "" {
		str = prefix + " > " + str
	}
	pathStringsCache[last] = str
	return str
}

func (p path) string() string {
	rs := []string{}
	for _, n := range p {
		rs = append(rs, n.string())
	}
	return strings.Join(rs, " > ")
}

// distance calculates the levenshtein distance between the string represention
// of two paths
func (p path) distance(p2 path) float64 {
	return float64(levenshtein.ComputeDistance(p.string(), p2.string()))
}

// Analyzer contains all the necessary config parameters and structs needed
// to analyze the webpage.
type Analyzer struct {
	Tokenizer   *html.Tokenizer
	LocMan      locationManager
	PagMan      locationManager
	NextPaths   locationManager
	NumChildren map[string]int    // the number of children a node (represented by a path) has, including non-html-tag nodes (ie text)
	ChildNodes  map[string][]node // the children of the node at the specified nodePath; used for :nth-child() logic
	NodePath    path
	Depth       int
	InBody      bool
	FindNext    bool

	currentAAttrs    map[string]string
	currentAText     *strings.Builder
	pathStringsCache map[*node]string
}

func (a *Analyzer) Parse() {
	a.pathStringsCache = map[*node]string{}
	// start analyzing the html
	for keepGoing := true; keepGoing; keepGoing = a.ParseToken(a.Tokenizer.Next()) {
	}
}

// ParseToken returns whether to keep going with the parsing.
func (a *Analyzer) ParseToken(tt html.TokenType) bool {
	switch tt {
	case html.ErrorToken:
		return false

	case html.TextToken:
		if !a.InBody {
			return true
		}

		name := a.NodePath.last().tagName
		if scrape.DoPruning && scrape.SkipTag[name] {
			slog.Debug("in generate.Analyzer.ParseToken(), skipping", "name", name)
			return true
		}

		p := a.NodePath.memoString(a.pathStringsCache)
		// fmt.Printf("path: %q\n", p)
		text := string(a.Tokenizer.Text())
		// fmt.Printf("in Analyzer.ParseToken(tt: %s), text: %q\n", tt, text)
		textTrimmed := strings.TrimSpace(text)
		if len(textTrimmed) > 0 {
			// lp := makeLocationProps(a.NodePath[:len(a.NodePath)-1], textTrimmed)
			lp := makeLocationProps(a.NodePath, textTrimmed, true)
			lp.textIndex = a.NumChildren[p]
			a.LocMan = append(a.LocMan, &lp)
		}
		a.NumChildren[p] += 1

		if a.currentAAttrs != nil {
			a.currentAText.WriteString(text)
		}

	case html.StartTagToken, html.EndTagToken:

		tn, _ := a.Tokenizer.TagName()
		name := string(tn)
		if name == "body" {
			a.InBody = !a.InBody
		}
		if !a.InBody {
			return true
		}

		p := a.NodePath.memoString(a.pathStringsCache)

		// fmt.Printf("in Analyzer.ParseToken(tt: %s), tag name: %q\n", tt, tagNameStr)
		// br can also be self closing tag, see later case statement
		if name == "br" || name == "input" {
			a.NumChildren[p] += 1
			a.ChildNodes[p] = append(a.ChildNodes[p], node{tagName: name})
			return true
		}

		if tt != html.StartTagToken {
			if a.currentAAttrs != nil {
				// fmt.Printf("looking for pagination candidate %q, %#v\n", tagNameStr, currentAAttrs) //, a.NodePath)
				href := a.currentAAttrs["href"]
				lp := makeLocationProps(a.NodePath, href, false)
				if strings.ToLower(a.currentAAttrs["aria-label"]) == "next" {
					// fmt.Printf("found pagination candidate %q, %#v\n", tagNameStr, attrs) // , a.NodePath)
					a.NextPaths = append(a.NextPaths, &lp)
				} else if strings.ToLower(a.currentAText.String()) == "next" {
					a.NextPaths = append(a.NextPaths, &lp)
				} else {
					// text := string(a.Tokenizer.Text())
					a.PagMan = append(a.PagMan, &lp)
				}
				a.currentAAttrs = nil
				a.currentAText = nil
			}

			n := true
			for n && a.Depth > 0 {
				if a.NodePath[len(a.NodePath)-1].tagName == name {
					if name == "body" {
						return false
					}
					n = false
				}
				delete(a.NumChildren, p)
				delete(a.ChildNodes, p)
				a.NodePath = a.NodePath[:len(a.NodePath)-1]
				a.Depth--
			}
			return true
		}

		attrs, cls, pCls := getTagMetadata(name, a.Tokenizer, a.ChildNodes[p])
		a.NumChildren[p] += 1
		a.ChildNodes[p] = append(a.ChildNodes[p], node{tagName: name, classes: cls})

		newNode := node{
			tagName:       name,
			classes:       cls,
			pseudoClasses: pCls,
		}
		// fmt.Printf("newNode: %#v\n", newNode)
		a.NodePath = append(a.NodePath, newNode)
		a.Depth++
		a.ChildNodes[p] = []node{}

		for attrKey, attrValue := range attrs {
			lp := makeLocationProps(a.NodePath, attrValue, false)
			lp.attr = attrKey
			// fmt.Printf("lp: %#v\n", lp)
			a.LocMan = append(a.LocMan, &lp)
		}

		if a.FindNext {
			if name == "a" && attrs["href"] != "" {
				a.currentAAttrs = attrs
				a.currentAText = &strings.Builder{}
			}
		}
		// if tagNameStr == "a" {
		// 	// fmt.Printf("looking for pagination candidate %q, %#v\n", tagNameStr, attrs) //, a.NodePath)
		// 	for attrKey, attrValue := range attrs {
		// 		if attrKey == "href" {
		// 			lp := makeLocationProps(a.NodePath, attrValue)
		// 			a.PagMan = append(a.PagMan, &lp)
		// 		}
		// 		if attrKey == "aria-label" && strings.ToLower(attrValue) == "next" {
		// 			fmt.Printf("found pagination candidate %q, %#v\n", tagNameStr, attrs) // , a.NodePath)
		// 		}
		// 	}
		// }

	case html.SelfClosingTagToken:
		if !a.InBody {
			return true
		}

		tn, _ := a.Tokenizer.TagName()
		tagNameStr := string(tn)
		// fmt.Printf("in Analyzer.ParseToken(tt: %s), tag name: %q\n", tt, tagNameStr)

		if tagNameStr != "br" && tagNameStr != "input" && tagNameStr != "img" && tagNameStr != "link" {
			return true
		}

		p := a.NodePath.memoString(a.pathStringsCache)
		attrs, cls, pCls := getTagMetadata(tagNameStr, a.Tokenizer, a.ChildNodes[p])
		a.NumChildren[p] += 1
		a.ChildNodes[p] = append(a.ChildNodes[p], node{tagName: tagNameStr, classes: cls})
		if len(attrs) == 0 {
			return true
		}

		tmpNodePath := make([]node, len(a.NodePath))
		copy(tmpNodePath, a.NodePath)
		newNode := node{
			tagName:       tagNameStr,
			classes:       cls,
			pseudoClasses: pCls,
		}
		tmpNodePath = append(tmpNodePath, newNode)

		for attrKey, attrValue := range attrs {
			lp := makeLocationProps(tmpNodePath, attrValue, false)
			lp.attr = attrKey
			a.LocMan = append(a.LocMan, &lp)
		}
	}
	return true
}

var spacesRE = regexp.MustCompile(`\s+`)

// getTagMetadata, for a given node returns a map of key value pairs (only for the attriutes we're interested in) and
// a list of this node's classes and a list of this node's pseudo classes (currently only nth-child).
func getTagMetadata(tagName string, z *html.Tokenizer, siblingNodes []node) (map[string]string, []string, []string) {
	allowedAttrs := map[string]map[string]bool{
		"a":   {"href": true, "aria-label": true},
		"img": {"src": true},
	}
	moreAttr := true
	attrs := make(map[string]string)
	var cls []string       // classes
	if tagName != "body" { // we don't care about classes for the body tag
		for moreAttr {
			k, v, m := z.TagAttr()
			vString := strings.TrimSpace(string(v))
			kString := string(k)
			if kString == "class" && vString != "" {
				cls = spacesRE.Split(vString, -1)
				j := 0
				for _, cl := range cls {
					// for now we ignore classes that contain dots
					if cl != "" && !strings.Contains(cl, ".") {
						cls[j] = cl
						j++
					}
				}
				cls = cls[:j]
			}
			if _, found := allowedAttrs[tagName]; found {
				if _, found := allowedAttrs[tagName][kString]; found {
					attrs[kString] = vString
				}
			}
			moreAttr = m
		}
	}
	var pCls []string // pseudo classes
	// only add nth-child if there has been another node before at the same
	// level (sibling node) with same tag and the same classes
	for i := 0; i < len(siblingNodes); i++ {
		childNode := siblingNodes[i]
		if childNode.tagName == tagName {
			if utils.SliceEquals(childNode.classes, cls) {
				pCls = []string{fmt.Sprintf("nth-child(%d)", len(siblingNodes)+1)}
				break
			}
		}

	}
	return attrs, cls, pCls
}
