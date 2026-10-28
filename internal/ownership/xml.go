package ownership

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

const maxXMLDepth = 128

type xmlElement struct {
	name     string
	attrs    map[string]string
	children []*xmlElement
	content  strings.Builder
}

func decodeXML(ctx context.Context, reader io.Reader) (*xmlElement, error) {
	decoder := xml.NewDecoder(reader)

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}

		switch value := token.(type) {
		case xml.StartElement:
			return readXMLElement(ctx, decoder, value, 0)
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return nil, errors.New("text before XML root")
			}
		case xml.Directive:
			return nil, errors.New("XML directives are not supported")
		}
	}
}

func readXMLElement(ctx context.Context, decoder *xml.Decoder,
	start xml.StartElement, depth int,
) (*xmlElement, error) {
	if depth > maxXMLDepth {
		return nil, fmt.Errorf("XML nesting exceeds %d elements", maxXMLDepth)
	}

	element := &xmlElement{
		name:  start.Name.Local,
		attrs: make(map[string]string, len(start.Attr)),
	}
	for _, attribute := range start.Attr {
		element.attrs[attribute.Name.Local] = attribute.Value
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}

		switch value := token.(type) {
		case xml.StartElement:
			child, err := readXMLElement(ctx, decoder, value, depth+1)
			if err != nil {
				return nil, err
			}

			element.children = append(element.children, child)
		case xml.CharData:
			element.content.Write(value)
		case xml.EndElement:
			return element, nil
		case xml.Directive:
			return nil, errors.New("XML directives are not supported")
		}
	}
}

func (element *xmlElement) child(name string) *xmlElement {
	if element == nil {
		return nil
	}

	for _, child := range element.children {
		if child.name == name {
			return child
		}
	}

	return nil
}

func (element *xmlElement) childrenNamed(name string) []*xmlElement {
	if element == nil {
		return nil
	}

	children := make([]*xmlElement, 0)
	for _, child := range element.children {
		if child.name == name {
			children = append(children, child)
		}
	}

	return children
}

func (element *xmlElement) descendants(name string) []*xmlElement {
	if element == nil {
		return nil
	}

	var result []*xmlElement

	for _, child := range element.children {
		if child.name == name {
			result = append(result, child)
		}

		result = append(result, child.descendants(name)...)
	}

	return result
}

func (element *xmlElement) path(names ...string) *xmlElement {
	current := element
	for _, name := range names {
		current = current.child(name)
		if current == nil {
			return nil
		}
	}

	return current
}

func (element *xmlElement) text() string {
	if element == nil {
		return ""
	}

	return strings.TrimSpace(element.content.String())
}

func (element *xmlElement) value(names ...string) string {
	value := element.path(names...)
	if value == nil {
		return ""
	}

	if wrapped := value.child("value"); wrapped != nil {
		return wrapped.text()
	}

	return value.text()
}

func parseBoolean(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "y", "yes":
		return true
	default:
		return false
	}
}
