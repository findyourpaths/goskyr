package autoconfig

import (
	"strings"

	"golang.org/x/net/html"
)

// Analyzer contains all the necessary config parameters and structs needed
// to analyze the webpage.
type Analyzer struct {
	Tokenizer   *html.Tokenizer
	LocMan      locationManager
	NumChildren map[string]int    // the number of children a node (represented by a path) has, including non-html-tag nodes (ie text)
	ChildNodes  map[string][]node // the children of the node at the specified nodePath; used for :nth-child() logic
	NodePath    path
	Depth       int
	InBody      bool
}

func (a *Analyzer) Parse() {
	// start analyzing the html
	for keepGoing := true; keepGoing; keepGoing = a.ParseToken(a.Tokenizer.Next()) {
	}
}

func (a *Analyzer) ParseToken(tt html.TokenType) bool {
	switch tt {
	case html.ErrorToken:
		return false

	case html.TextToken:
		if !a.InBody {
			return true
		}

		p := a.NodePath.string()
		text := string(a.Tokenizer.Text())
		// fmt.Printf("in Analyzer.ParseToken(tt: %s), text: %q\n", tt, text)
		textTrimmed := strings.TrimSpace(text)
		if len(textTrimmed) > 0 {
			lp := makeLocationProps(a.NodePath, textTrimmed)
			lp.textIndex = a.NumChildren[p]
			a.LocMan = append(a.LocMan, &lp)
		}
		a.NumChildren[p] += 1

	case html.StartTagToken, html.EndTagToken:
		tn, _ := a.Tokenizer.TagName()
		tagNameStr := string(tn)
		if tagNameStr == "body" {
			a.InBody = !a.InBody
		}
		if !a.InBody {
			return true
		}

		// fmt.Printf("in Analyzer.ParseToken(tt: %s), tag name: %q\n", tt, tagNameStr)
		// br can also be self closing tag, see later case statement
		if tagNameStr == "br" || tagNameStr == "input" {
			a.NumChildren[a.NodePath.string()] += 1
			a.ChildNodes[a.NodePath.string()] = append(a.ChildNodes[a.NodePath.string()], node{tagName: tagNameStr})
			return true
		}

		if tt != html.StartTagToken {
			n := true
			for n && a.Depth > 0 {
				if a.NodePath[len(a.NodePath)-1].tagName == tagNameStr {
					if tagNameStr == "body" {
						return false
					}
					n = false
				}
				delete(a.NumChildren, a.NodePath.string())
				delete(a.ChildNodes, a.NodePath.string())
				a.NodePath = a.NodePath[:len(a.NodePath)-1]
				a.Depth--
			}
			return true
		}

		attrs, cls, pCls := getTagMetadata(tagNameStr, a.Tokenizer, a.ChildNodes[a.NodePath.string()])
		a.NumChildren[a.NodePath.string()] += 1
		a.ChildNodes[a.NodePath.string()] = append(a.ChildNodes[a.NodePath.string()], node{tagName: tagNameStr, classes: cls})

		newNode := node{
			tagName:       tagNameStr,
			classes:       cls,
			pseudoClasses: pCls,
		}
		// fmt.Printf("newNode: %#v\n", newNode)
		a.NodePath = append(a.NodePath, newNode)
		a.Depth++
		a.ChildNodes[a.NodePath.string()] = []node{}

		for attrKey, attrValue := range attrs {
			lp := makeLocationProps(a.NodePath, attrValue)
			lp.attr = attrKey
			// fmt.Printf("lp: %#v\n", lp)
			a.LocMan = append(a.LocMan, &lp)
		}

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

		attrs, cls, pCls := getTagMetadata(tagNameStr, a.Tokenizer, a.ChildNodes[a.NodePath.string()])
		a.NumChildren[a.NodePath.string()] += 1
		a.ChildNodes[a.NodePath.string()] = append(a.ChildNodes[a.NodePath.string()], node{tagName: tagNameStr, classes: cls})
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
			lp := makeLocationProps(tmpNodePath, attrValue)
			lp.attr = attrKey
			a.LocMan = append(a.LocMan, &lp)
		}
	}
	return true
}
