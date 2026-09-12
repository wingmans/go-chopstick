package edgar

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"maps"
	"strings"
)

const (
	xbrlNamespace      = "http://www.xbrl.org/2003/instance"
	linkNamespace      = "http://www.xbrl.org/2003/linkbase"
	dimensionNamespace = "http://xbrl.org/2006/xbrldi"
	xsiNamespace       = "http://www.w3.org/2001/XMLSchema-instance"
	xmlNamespace       = "http://www.w3.org/XML/1998/namespace"
)

func newXMLNode(start xml.StartElement, namespaces map[string]string, language string) XMLNode {
	var node XMLNode

	node.Name = QName{Namespace: start.Name.Space, Local: start.Name.Local}
	node.Language = language

	node.Namespaces = maps.Clone(namespaces)
	if node.Namespaces == nil {
		node.Namespaces = make(map[string]string)
	}

	node.Namespaces["xml"] = xmlNamespace

	for _, attribute := range start.Attr {
		switch {
		case attribute.Name.Space == "xmlns":
			node.Namespaces[attribute.Name.Local] = attribute.Value
		case attribute.Name.Space == "" && attribute.Name.Local == "xmlns":
			node.Namespaces[""] = attribute.Value
		default:
			node.Attributes = append(node.Attributes, XMLAttribute{
				Name: QName{Namespace: attribute.Name.Space, Local: attribute.Name.Local}, Value: attribute.Value,
			})
		}

		if attribute.Name.Space == xmlNamespace && attribute.Name.Local == "lang" {
			node.Language = attribute.Value
		}
	}

	return node
}

func readXMLNode(ctx context.Context, decoder *xml.Decoder, start xml.StartElement, parent XMLNode, depth int) (XMLNode, error) {
	if depth > 256 {
		return XMLNode{}, errors.New("XML nesting exceeds 256 elements")
	}

	node := newXMLNode(start, parent.Namespaces, parent.Language)

	for {
		if err := ctx.Err(); err != nil {
			return XMLNode{}, err
		}

		token, err := decoder.Token()
		if err != nil {
			return XMLNode{}, err
		}

		switch value := token.(type) {
		case xml.StartElement:
			child, err := readXMLNode(ctx, decoder, value, node, depth+1)
			if err != nil {
				return XMLNode{}, err
			}

			node.Content = append(node.Content, XMLContent{Text: "", Element: &child})
		case xml.CharData:
			node.Content = append(node.Content, XMLContent{Text: string(value), Element: nil})
		case xml.EndElement:
			return node, nil
		case xml.Directive:
			return XMLNode{}, errors.New("XML directives are not supported")
		}
	}
}

func (n *XMLNode) attribute(namespace, name string) string {
	for _, attribute := range n.Attributes {
		if attribute.Name.Namespace == namespace && attribute.Name.Local == name {
			return attribute.Value
		}
	}

	return ""
}

func (n *XMLNode) text() string {
	var result strings.Builder
	for _, content := range n.Content {
		result.WriteString(content.Text)

		if content.Element != nil {
			result.WriteString(content.Element.text())
		}
	}

	return result.String()
}

func (n *XMLNode) instanceChild(name string) *XMLNode {
	for _, content := range n.Content {
		if content.Element != nil && content.Element.Name == (QName{Namespace: xbrlNamespace, Local: name}) {
			return content.Element
		}
	}

	return nil
}

func (n *XMLNode) childText(name string) string {
	if child := n.instanceChild(name); child != nil {
		return strings.TrimSpace(child.text())
	}

	return ""
}

func (n *XMLNode) resolveQName(value string) (QName, error) {
	value = strings.TrimSpace(value)

	prefix, local, qualified := strings.Cut(value, ":")
	if qualified && prefix == "" {
		return QName{}, fmt.Errorf("invalid QName %q", value)
	}

	if !qualified {
		local, prefix = value, ""
	}

	namespace, declared := n.Namespaces[prefix]
	if local == "" || strings.ContainsAny(local, ": \t\r\n") || (prefix != "" && (!declared || namespace == "")) {
		return QName{}, fmt.Errorf("invalid or unbound QName %q", value)
	}

	return QName{Namespace: namespace, Local: local}, nil
}

func firstXMLElement(decoder *xml.Decoder) (xml.StartElement, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return xml.StartElement{}, err
		}

		switch value := token.(type) {
		case xml.StartElement:
			return value, nil
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return xml.StartElement{}, errors.New("text before XML root")
			}
		case xml.Directive:
			return xml.StartElement{}, errors.New("XML directives are not supported")
		}
	}
}
