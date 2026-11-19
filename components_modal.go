package discordgo

// ModalChoice is an option in a RadioGroup or CheckboxGroup.
type ModalChoice struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// RadioGroup lets a user choose one option in a modal. Place it inside a Label.
type RadioGroup struct {
	ID       int           `json:"id,omitempty"`
	CustomID string        `json:"custom_id"`
	Options  []ModalChoice `json:"options,omitempty"`
	Required *bool         `json:"required,omitempty"`

	// Value is populated on submission. A nil value means no option was selected.
	Value *string `json:"value,omitempty"`
}

func (RadioGroup) Type() ComponentType { return RadioGroupComponent }

func (r RadioGroup) MarshalJSON() ([]byte, error) {
	type radioGroup RadioGroup
	return Marshal(struct {
		radioGroup
		Type ComponentType `json:"type"`
	}{radioGroup(r), r.Type()})
}

// CheckboxGroup lets a user choose multiple options in a modal. Place it inside a Label.
type CheckboxGroup struct {
	ID        int           `json:"id,omitempty"`
	CustomID  string        `json:"custom_id"`
	Options   []ModalChoice `json:"options,omitempty"`
	Required  *bool         `json:"required,omitempty"`
	MinValues *int          `json:"min_values,omitempty"`
	MaxValues int           `json:"max_values,omitempty"`

	// Values is populated on submission and is empty when nothing was selected.
	Values []string `json:"values,omitempty"`
}

func (CheckboxGroup) Type() ComponentType { return CheckboxGroupComponent }

func (c CheckboxGroup) MarshalJSON() ([]byte, error) {
	type checkboxGroup CheckboxGroup
	return Marshal(struct {
		checkboxGroup
		Type ComponentType `json:"type"`
	}{checkboxGroup(c), c.Type()})
}

// Checkbox lets a user answer a yes/no question in a modal. Place it inside a Label.
type Checkbox struct {
	ID       int    `json:"id,omitempty"`
	CustomID string `json:"custom_id"`
	Default  bool   `json:"default,omitempty"`

	// Value is populated on submission, including when the checkbox is unchecked.
	Value *bool `json:"value,omitempty"`
}

func (Checkbox) Type() ComponentType { return CheckboxComponent }

func (c Checkbox) MarshalJSON() ([]byte, error) {
	type checkbox Checkbox
	return Marshal(struct {
		checkbox
		Type ComponentType `json:"type"`
	}{checkbox(c), c.Type()})
}
