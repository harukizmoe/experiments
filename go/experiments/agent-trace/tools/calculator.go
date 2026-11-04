package agent_trace_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
)

type CalculatorTool struct{}

func (CalculatorTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: "calculator",

		Description: "Evaluate a basic arithmetic expression using +, -, *, / and parentheses. Use it when exact arithmetic is useful.",

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"expression": map[string]any{
					"type": "string",

					"description": "Arithmetic expression, for example: (129 * 3) + 20.",
				},
			},

			"required": []string{
				"expression",
			},

			"additionalProperties": false,
		},
	}
}

func (CalculatorTool) Execute(
	_ context.Context,
	arguments json.RawMessage,
) (string, error) {

	var args struct {
		Expression string `json:"expression"`
	}

	if err := json.Unmarshal(
		arguments,
		&args,
	); err != nil {

		return "", fmt.Errorf(
			"decode calculator arguments: %w",
			err,
		)
	}

	args.Expression = strings.TrimSpace(
		args.Expression,
	)

	if args.Expression == "" {
		return "", fmt.Errorf(
			"expression is required",
		)
	}

	value, err := evaluateExpression(
		args.Expression,
	)
	if err != nil {
		return "", err
	}

	if math.IsInf(value, 0) ||
		math.IsNaN(value) {

		return "", fmt.Errorf(
			"expression produced a non-finite result",
		)
	}

	return marshalJSON(
		map[string]any{
			"expression": args.Expression,
			"result":     value,
		},
	)
}

type expressionParser struct {
	input []rune
	pos   int
}

func evaluateExpression(
	input string,
) (float64, error) {

	p := &expressionParser{
		input: []rune(input),
	}

	value, err := p.parseExpression()
	if err != nil {
		return 0, err
	}

	p.skipSpaces()

	if p.pos != len(p.input) {
		return 0, fmt.Errorf(
			"unexpected token %q at position %d",
			p.input[p.pos],
			p.pos,
		)
	}

	return value, nil
}

func (p *expressionParser) parseExpression() (
	float64,
	error,
) {

	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}

	for {
		p.skipSpaces()

		if p.match('+') {
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}

			left += right
			continue
		}

		if p.match('-') {
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}

			left -= right
			continue
		}

		return left, nil
	}
}

func (p *expressionParser) parseTerm() (
	float64,
	error,
) {

	left, err := p.parseFactor()
	if err != nil {
		return 0, err
	}

	for {
		p.skipSpaces()

		if p.match('*') {
			right, err := p.parseFactor()
			if err != nil {
				return 0, err
			}

			left *= right
			continue
		}

		if p.match('/') {
			right, err := p.parseFactor()
			if err != nil {
				return 0, err
			}

			if right == 0 {
				return 0, fmt.Errorf(
					"division by zero",
				)
			}

			left /= right
			continue
		}

		return left, nil
	}
}

func (p *expressionParser) parseFactor() (
	float64,
	error,
) {

	p.skipSpaces()

	if p.match('+') {
		return p.parseFactor()
	}

	if p.match('-') {
		value, err := p.parseFactor()
		return -value, err
	}

	if p.match('(') {
		value, err := p.parseExpression()
		if err != nil {
			return 0, err
		}

		p.skipSpaces()

		if !p.match(')') {
			return 0, fmt.Errorf(
				"missing closing parenthesis at position %d",
				p.pos,
			)
		}

		return value, nil
	}

	return p.parseNumber()
}

func (p *expressionParser) parseNumber() (
	float64,
	error,
) {

	p.skipSpaces()

	start := p.pos
	dotSeen := false

	for p.pos < len(p.input) {
		r := p.input[p.pos]

		if unicode.IsDigit(r) {
			p.pos++
			continue
		}

		if r == '.' && !dotSeen {
			dotSeen = true
			p.pos++
			continue
		}

		break
	}

	if start == p.pos {
		if p.pos >= len(p.input) {
			return 0, fmt.Errorf(
				"expected number at end of expression",
			)
		}

		return 0, fmt.Errorf(
			"expected number at position %d",
			p.pos,
		)
	}

	text := string(
		p.input[start:p.pos],
	)

	value, err := strconv.ParseFloat(
		text,
		64,
	)

	if err != nil {
		return 0, fmt.Errorf(
			"invalid number %q: %w",
			text,
			err,
		)
	}

	return value, nil
}

func (p *expressionParser) skipSpaces() {
	for p.pos < len(p.input) &&
		unicode.IsSpace(p.input[p.pos]) {

		p.pos++
	}
}

func (p *expressionParser) match(
	expected rune,
) bool {

	if p.pos < len(p.input) &&
		p.input[p.pos] == expected {

		p.pos++
		return true
	}

	return false
}
