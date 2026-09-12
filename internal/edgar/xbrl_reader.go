package edgar

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

func readXBRL(ctx context.Context, source io.Reader) (XBRLInstance, bool, error) {
	var instance XBRLInstance

	decoder := xml.NewDecoder(source)

	start, err := firstXMLElement(decoder)
	if err != nil {
		return instance, false, err
	}

	if start.Name.Space != xbrlNamespace || start.Name.Local != "xbrl" {
		return instance, false, nil
	}

	root := newXMLNode(start, nil, "")
	instance.Root = root
	closed := false

	for {
		if err := ctx.Err(); err != nil {
			return XBRLInstance{}, true, err
		}

		token, err := decoder.Token()
		if errors.Is(err, io.EOF) && closed {
			return instance, true, nil
		}

		if err != nil {
			return XBRLInstance{}, true, err
		}

		switch value := token.(type) {
		case xml.StartElement:
			if closed {
				return XBRLInstance{}, true, errors.New("multiple XML roots")
			}

			node, err := readXMLNode(ctx, decoder, value, root, 1)
			if err != nil {
				return XBRLInstance{}, true, err
			}

			if err := instance.addNode(node); err != nil {
				return XBRLInstance{}, true, err
			}
		case xml.EndElement:
			closed = true
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return XBRLInstance{}, true, errors.New("unexpected text outside XBRL item")
			}
		case xml.Directive:
			return XBRLInstance{}, true, errors.New("XML directives are not supported")
		}
	}
}

func (x *XBRLInstance) addNode(node XMLNode) error {
	switch node.Name {
	case QName{Namespace: xbrlNamespace, Local: "context"}:
		value, err := parseFactContext(node)
		if err != nil {
			return err
		}

		x.Contexts = append(x.Contexts, value)
	case QName{Namespace: xbrlNamespace, Local: "unit"}:
		value, err := parseFactUnit(node)
		if err != nil {
			return err
		}

		x.Units = append(x.Units, value)
	case QName{Namespace: linkNamespace, Local: "schemaRef"}, QName{Namespace: linkNamespace, Local: "linkbaseRef"}:
		x.References = append(x.References, node)
	case QName{Namespace: linkNamespace, Local: "footnoteLink"}:
		x.Footnotes = append(x.Footnotes, node)
	default:
		if node.attribute("", "contextRef") == "" {
			x.Unsupported = append(x.Unsupported, node)

			return nil
		}

		fact, err := parseFact(node)
		if err != nil {
			return err
		}

		x.Facts = append(x.Facts, fact)
	}

	return nil
}

func parseFact(node XMLNode) (Fact, error) {
	var fact Fact

	fact.Concept = node.Name
	fact.Namespaces = node.Namespaces
	fact.Value = node.text()
	fact.ContextRef = node.attribute("", "contextRef")
	fact.UnitRef = node.attribute("", "unitRef")
	fact.Decimals = node.attribute("", "decimals")
	fact.Precision = node.attribute("", "precision")
	fact.ID = node.attribute("", "id")
	fact.Language = node.Language

	fact.Attributes = node.Attributes
	switch node.attribute(xsiNamespace, "nil") {
	case "true", "1":
		fact.Nil = true
	case "", "false", "0":
	default:
		return Fact{}, errors.New("invalid xsi:nil value")
	}

	for _, content := range node.Content {
		if content.Element != nil {
			fact.Structured = &node

			break
		}
	}

	if fact.Nil && (strings.TrimSpace(fact.Value) != "" || fact.Structured != nil) {
		return Fact{}, errors.New("nil fact has content")
	}

	return fact, nil
}

func parseFactContext(node XMLNode) (FactContext, error) {
	var result FactContext

	result.ID, result.Source = node.attribute("", "id"), node

	entity, period := node.instanceChild("entity"), node.instanceChild("period")
	if result.ID == "" || entity == nil || period == nil {
		return result, errors.New("context missing id, entity, or period")
	}

	identifier := entity.instanceChild("identifier")
	if identifier == nil {
		return result, errors.New("context missing entity identifier")
	}

	result.Entity, result.Scheme = strings.TrimSpace(identifier.text()), identifier.attribute("", "scheme")
	result.Instant, result.StartDate, result.EndDate = period.childText("instant"), period.childText("startDate"), period.childText("endDate")

	result.Forever = period.instanceChild("forever") != nil
	if !validContextPeriod(result) {
		return result, errors.New("context must have an instant, a duration, or forever")
	}

	for _, container := range []*XMLNode{entity.instanceChild("segment"), node.instanceChild("scenario")} {
		if container == nil {
			continue
		}

		for _, content := range container.Content {
			member := content.Element
			if member == nil || member.Name.Namespace != dimensionNamespace {
				continue
			}

			if member.Name.Local != "explicitMember" && member.Name.Local != "typedMember" {
				continue
			}

			dimension, err := parseDimension(member)
			if err != nil {
				return result, err
			}

			result.Dimensions = append(result.Dimensions, dimension)
		}
	}

	return result, nil
}

func validContextPeriod(value FactContext) bool {
	if value.Forever {
		return value.Instant == "" && value.StartDate == "" && value.EndDate == ""
	}

	if value.Instant != "" {
		return value.StartDate == "" && value.EndDate == ""
	}

	return value.StartDate != "" && value.EndDate != ""
}

func parseDimension(node *XMLNode) (FactDimension, error) {
	var result FactDimension

	axis, err := node.resolveQName(node.attribute("", "dimension"))
	if err != nil {
		return result, err
	}

	result.Axis = axis
	if node.Name.Local == "typedMember" {
		result.Typed = node
	} else {
		member, err := node.resolveQName(node.text())
		if err != nil {
			return result, err
		}

		result.Member = &member
	}

	return result, nil
}

func parseFactUnit(node XMLNode) (FactUnit, error) {
	var result FactUnit

	result.ID, result.Source = node.attribute("", "id"), node
	if result.ID == "" {
		return result, errors.New("unit missing id")
	}

	divide := node.instanceChild("divide")
	if divide == nil {
		measures, err := unitMeasures(&node)
		result.Measures = measures

		return result, err
	}

	numerator, denominator := divide.instanceChild("unitNumerator"), divide.instanceChild("unitDenominator")
	if numerator == nil || denominator == nil {
		return result, errors.New("divided unit missing numerator or denominator")
	}

	var err error

	result.Numerator, err = unitMeasures(numerator)
	if err != nil {
		return result, err
	}

	result.Denominator, err = unitMeasures(denominator)
	if err != nil {
		return result, err
	}

	return result, nil
}

func unitMeasures(node *XMLNode) ([]QName, error) {
	var result []QName

	for _, content := range node.Content {
		if content.Element == nil || content.Element.Name != (QName{Namespace: xbrlNamespace, Local: "measure"}) {
			continue
		}

		measure, err := content.Element.resolveQName(content.Element.text())
		if err != nil {
			return nil, err
		}

		result = append(result, measure)
	}

	if len(result) == 0 {
		return nil, errors.New("unit missing measures")
	}

	return result, nil
}

func (x *XBRLInstance) referenceErrors() []string {
	var result []string

	contexts, units, ids := make(map[string]bool), make(map[string]bool), make(map[string]bool)

	checkID := func(id string) {
		if id == "" {
			return
		}

		if ids[id] {
			result = append(result, fmt.Sprintf("duplicate XML id %q", id))
		}

		ids[id] = true
	}
	for _, value := range x.Contexts {
		checkID(value.ID)
		contexts[value.ID] = true
	}

	for _, value := range x.Units {
		checkID(value.ID)
		units[value.ID] = true
	}

	for _, fact := range x.Facts {
		checkID(fact.ID)

		if !contexts[fact.ContextRef] {
			result = append(result, fmt.Sprintf("fact %s references missing context %q", fact.Concept.Local, fact.ContextRef))
		}

		if fact.UnitRef != "" && !units[fact.UnitRef] {
			result = append(result, fmt.Sprintf("fact %s references missing unit %q", fact.Concept.Local, fact.UnitRef))
		}
	}

	return result
}
