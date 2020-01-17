package core

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/scaleway/scaleway-sdk-go/logger"
)

// AutocompleteSuggestions is a list of words to be set to the shell as autocomplete suggestions.
type AutocompleteSuggestions []string

// AutocompleteResponse contains the autocomplete suggestions
type AutocompleteResponse struct {
	Suggestions AutocompleteSuggestions
}

const variableFlagValueSuffix = "-value"

type AutoCompleteNodeType int

const (
	AutoCompleteNodeTypeCommand = iota
	AutoCompleteNodeTypeArgument
	AutoCompleteNodeTypeFlag
	AutoCompleteNodeTypeFlagValueConst
	AutoCompleteNodeTypeFlagValueVariable
)

// AutoCompleteArgFunc is the function called to complete arguments values.
// It is retrieved from core.ArgSpec.AutoCompleteFunc.
type AutoCompleteArgFunc func(ctx context.Context, prefix string) AutocompleteSuggestions

// AutoCompleteNode is a node in the AutoComplete Tree.
// An AutoCompleteNode can either represent a command, a subcommand, or a command argument.
type AutoCompleteNode struct {
	Children map[string]*AutoCompleteNode
	Command  *Command
	ArgSpec  *ArgSpec
	Type     AutoCompleteNodeType

	// Name of the current node. Useful for debugging.
	Name string
}

func (n *AutoCompleteNode) addGlobalFlags() {
	n.Children["--access-key"] = NewFlagAutoCompleteNode("--access-key", n, nil, true)
	n.Children["-D"] = NewFlagAutoCompleteNode("-D", n, nil, false)
	n.Children["--debug"] = NewFlagAutoCompleteNode("--debug", n, nil, false)
	n.Children["-h"] = NewFlagAutoCompleteNode("-h", n, nil, false)
	n.Children["--help"] = NewFlagAutoCompleteNode("--help", n, nil, false)
	n.Children["-o"] = NewFlagAutoCompleteNode("-o", n, []string{"json", "human"}, false)
	n.Children["--output"] = NewFlagAutoCompleteNode("--output", n, []string{"json", "human"}, false)
	n.Children["-p"] = NewFlagAutoCompleteNode("-p", n, nil, false)
	n.Children["--profile"] = NewFlagAutoCompleteNode("--profile", n, nil, false)
	n.Children["--secret-key"] = NewFlagAutoCompleteNode("--secret-key", n, nil, true)
}

// newAutoCompleteResponse builds a new AutocompleteResponse
func newAutoCompleteResponse(suggestions []string) *AutocompleteResponse {
	sort.Strings(suggestions)
	return &AutocompleteResponse{
		Suggestions: suggestions,
	}
}

// NewAutoCompleteNode creates a new node corresponding to a command or subcommand.
// These nodes are not necessarily leaf nodes.
func NewAutoCompleteNode() *AutoCompleteNode {
	return &AutoCompleteNode{
		Children: map[string]*AutoCompleteNode{},
		Type:     AutoCompleteNodeTypeCommand,
	}
}

// NewArgAutoCompleteNode creates a new node corresponding to a command argument.
// These nodes are leaf nodes.
func NewArgAutoCompleteNode(argSpec *ArgSpec) *AutoCompleteNode {
	node := NewAutoCompleteNode()
	node.ArgSpec = argSpec
	node.Type = AutoCompleteNodeTypeArgument
	return node
}

func NewFlagAutoCompleteNode(name string, parent *AutoCompleteNode, values []string, hasVariableValue bool) *AutoCompleteNode {
	node := NewAutoCompleteNode()
	node.Type = AutoCompleteNodeTypeFlag
	node.Name = name
	for _, value := range values {
		node.Children[value] = NewAutoCompleteNode()
		node.Children[value].Children = parent.Children
		node.Children[value].Type = AutoCompleteNodeTypeFlagValueConst
	}
	if hasVariableValue {
		childName := name + variableFlagValueSuffix
		node.Children[childName] = NewAutoCompleteNode()
		node.Children[childName].Children = parent.Children
		node.Children[childName].Type = AutoCompleteNodeTypeFlagValueVariable
	}
	if len(node.Children) == 0 {
		node.Children = parent.Children
	}
	return node
}

// GetChildOrCreate search a child node by name,
// and either returns it if found
// or create a new child with the given name, and returns it.
func (node *AutoCompleteNode) GetChildOrCreate(name string) *AutoCompleteNode {
	if _, exist := node.Children[name]; !exist {
		node.Children[name] = NewAutoCompleteNode()
	}
	return node.Children[name]
}

// GetChildMatch returns a child for a node if the child exists for this node.
// 3 types of children are supported :
// - command:                             command
// - singular argument name:              argument=
// - plural argument name + alphanumeric: arguments.key1=
func (node *AutoCompleteNode) GetChildMatch(name string) (*AutoCompleteNode, bool) {
	for key, child := range node.Children {
		key = strings.ReplaceAll(key, sliceSchema, "[0-9]+")
		key = strings.ReplaceAll(key, mapSchema, "[0-9a-zA-Z-]+")
		r := regexp.MustCompile(key)
		if r.MatchString(name) {
			return child, true
		}
	}
	return nil, false
}

// isLeafCommand returns true only if n is a command (namespace or verb or resource) but has no child command
// a leaf command can have 2 types of children: arguments or flags
func (n *AutoCompleteNode) isLeafCommand() bool {
	if n.Type != AutoCompleteNodeTypeCommand {
		return false
	}
	for _, child := range n.Children {
		if child.Type == AutoCompleteNodeTypeCommand {
			return false
		}
	}
	return true
}

// BuildAutoCompleteTree builds the autocomplete tree from the commands, subcomands and arguments
func BuildAutoCompleteTree(commands *Commands) *AutoCompleteNode {
	root := NewAutoCompleteNode()
	scwCommand := root.GetChildOrCreate("scw")
	scwCommand.addGlobalFlags()
	for _, cmd := range commands.command {
		node := scwCommand

		// Creates nodes for namespaces, resources, verbs
		for _, part := range []string{cmd.Namespace, cmd.Resource, cmd.Verb} {
			if part != "" {
				node = node.GetChildOrCreate(part)
				node.addGlobalFlags()
			}
		}

		node.Command = cmd
		// We consider ArgSpecs as leaf in the autocomplete tree.
		for _, argSpec := range cmd.ArgSpecs {
			node.Children[argSpec.Name+"="] = NewArgAutoCompleteNode(argSpec)
		}

		if cmd.WaitFunc != nil {
			node.Children["-w"] = NewFlagAutoCompleteNode("-w", node, nil, false)
			node.Children["--wait"] = NewFlagAutoCompleteNode("--wait", node, nil, false)
		}
	}

	return root
}

// AutoComplete process a command line and returns autocompletion suggestions.
func AutoComplete(ctx context.Context, leftWords []string, wordToComplete string, rightWords []string) *AutocompleteResponse {
	commands := ExtractCommands(ctx)

	// Create AutoComplete Tree
	commandTreeRoot := BuildAutoCompleteTree(commands)

	// For each left word that is not a flag nor an argument, we try to go deeper in the autocomplete tree and store the current node in `node`.
	node := commandTreeRoot
	for i, word := range leftWords {
		children, childrenExists := node.Children[word]

		switch {
		case !childrenExists && node.isLeafCommand():
			// word is probably an unknown argument
			// Just skip it

		case !childrenExists:
			// We did not find a child matching exactly the word

			// Maybe we are in the special case where word==<variable value for a flag>
			previousWord := leftWords[i-1]
			children2, childrenExists2 := node.Children[previousWord+variableFlagValueSuffix]
			if childrenExists2 {
				node = children2
				break
			}

			// We did not reach a leaf command, and word is unknown
			return &AutocompleteResponse{}

		case children.Type == AutoCompleteNodeTypeArgument:
			// Do nothing
			// Arguments do not have children: they are not used to go deeper into the tree

		default:
			// word is a namespace or verb or resource or flag or flag value
			node = children
		}

	}

	// keep track of the completed args
	completedArgs := make(map[string]struct{})

	// keep track of the completed flags
	completedFlags := make(map[string]struct{})

	// We loop through all other words in order to find existing args and flags.
	// When a flag is found it populates `completedFlags`.
	// When an argument is found it populates `completedArgs`.
	for _, word := range append(leftWords, rightWords...) {
		logger.Debugf("word: '%v'", word)
		switch {

		// handle --flag=value and --flag
		case isFlag(word):
			completedFlags[wordKey(word)] = struct{}{}

		// handle arg=value
		case isArg(word):
			completedArgs[wordKey(word)+"="] = struct{}{}

		// handle boolean arg
		default:
			children, exist := node.Children[word+"="]
			if exist && children.Type == AutoCompleteNodeTypeArgument {
				completedArgs[word+"="] = struct{}{}
			}
		}
	}

	if isCompletingArgValue(wordToComplete) {
		argName, argValuePrefix := splitArgWord(wordToComplete)
		argNode, exist := node.GetChildMatch(argName)
		if !exist {
			// We try to complete the value of an unknown arg
			return &AutocompleteResponse{}
		}
		suggestions := AutoCompleteArgValue(ctx, argNode.ArgSpec, argValuePrefix)

		// We need to prefix suggestions with the argName to enable the arg value auto-completion.
		for k, s := range suggestions {
			suggestions[k] = argName + s
		}

		return newAutoCompleteResponse(suggestions)
	} else {
		// We are trying to complete a node: either a command name or an arg name or a flagname

		suggestions := []string(nil)
		for key, _ := range node.Children {
			if !hasPrefix(key, wordToComplete) {
				continue
			}

			switch {
			case strings.Contains(key, sliceSchema):
				suggestions = append(suggestions, keySuggestion(key, sliceSchema, completedArgs))
			case strings.Contains(key, mapSchema):
				suggestions = append(suggestions, keySuggestion(key, mapSchema, completedArgs))
			default:
				if _, exists := completedArgs[key]; exists {
					continue
				}
				if _, exists := completedFlags[key]; exists {
					continue
				}
				if isFlag(key) && wordToComplete == "" {
					// skip autocomplete flag on empty string
					// command: scw <tab>
					// suggestions: instance
					// command: scw -<tab>
					// suggestions: -o
					continue
				}
				suggestions = append(suggestions, key)
			}
		}

		return newAutoCompleteResponse(suggestions)
	}
}

// AutoCompleteArgValue returns suggestions for a (argument name, argument value prefix) pair.
// Priority is given to the AutoCompleteFunc from the ArgSpec, if it is set.
// Otherwise, we use EnumValues from the ArgSpec.
func AutoCompleteArgValue(ctx context.Context, argSpec *ArgSpec, argValuePrefix string) []string {
	if argSpec.AutoCompleteFunc != nil {
		return argSpec.AutoCompleteFunc(ctx, argValuePrefix)
	}
	suggestions := []string(nil)
	for _, value := range argSpec.EnumValues {
		if strings.HasPrefix(value, argValuePrefix) {
			suggestions = append(suggestions, value)
		}
	}
	return suggestions
}

func isCompletingArgValue(wordToComplete string) bool {
	wordParts := strings.SplitN(wordToComplete, "=", 2)
	return len(wordParts) == 2
}

func splitArgWord(wordToComplete string) (string, string) {
	wordParts := strings.SplitN(wordToComplete, "=", 2)
	return wordParts[0] + "=", wordParts[1]
}

func wordKey(word string) string {
	return strings.SplitN(word, "=", 2)[0]
}

func isArg(wordToComplete string) bool {
	return strings.Contains(wordToComplete, "=")
}

func isFlag(wordToComplete string) bool {
	return strings.HasPrefix(wordToComplete, "-")
}

// hasPrefix will look if the word to complete prefixes the given key.
// It also handle complexe key schema such as slices and maps. E.g.:
// `security-gr` prefixes `security-group-id`
// `image-ids.0` prefixes `image-ids.{index}`
// `volumes.0.s` prefixes `volumes.{index}.size`
// `ip.fr-par.c` prefixes `ip.{key}.class`
func hasPrefix(key, wordToComplete string) bool {
	switch {
	case strings.HasPrefix(key, wordToComplete):
		return true
	case !strings.Contains(wordToComplete, ".") && (strings.HasPrefix(key, sliceSchema) || strings.HasPrefix(key, mapSchema)):
		return true
	case !strings.Contains(key, ".") || !strings.Contains(wordToComplete, "."):
		return false
	}

	tmp := strings.SplitN(key, ".", 2)
	leftKey, rightKey := tmp[0], tmp[1]
	tmp = strings.SplitN(wordToComplete, ".", 2)
	leftWord, rightWord := tmp[0], tmp[1]
	if leftKey == leftWord || leftKey == sliceSchema || leftKey == mapSchema {
		return hasPrefix(rightKey, rightWord)
	}
	return false
}

// keySuggestion will suggest the next key available for the map (or array) argument.
// Keys are suggested in ascending order arg.0, arg.1, arg.2...
func keySuggestion(key, keySchema string, completedArg map[string]struct{}) string {
	key = strings.ReplaceAll(key, keySchema, "([0-9]+)")
	r := regexp.MustCompile(key)
	usedIndex := make(map[string]struct{})
	for arg := range completedArg {
		matches := r.FindStringSubmatch(arg)
		if len(matches) > 0 {
			usedIndex[matches[1]] = struct{}{}
		}
	}

	// try to find next available index
	i := 0
	for {
		_, exist := usedIndex[strconv.Itoa(i)]
		if !exist {
			break
		}
		i++
	}
	return strings.ReplaceAll(key, "([0-9]+)", strconv.Itoa(i))
}

func WordIndex(charIndex int, words []string) int {
	wordIndex := len(words)
	charCount := 0
	for i, word := range words {
		charCount += len(word)
		if charIndex <= charCount {
			wordIndex = i
			break
		}

		charCount++ // space character
	}
	return wordIndex
}
