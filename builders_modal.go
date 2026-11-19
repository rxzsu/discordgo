package discordgo

import "slices"

// RadioGroupBuilder constructs a modal radio group.
type RadioGroupBuilder struct {
	group RadioGroup
}

// NewRadioGroup starts a group with the given input ID.
func NewRadioGroup(customID string) *RadioGroupBuilder {
	return &RadioGroupBuilder{group: RadioGroup{CustomID: customID}}
}

// AddOption appends an available choice.
func (b *RadioGroupBuilder) AddOption(option ModalChoice) *RadioGroupBuilder {
	b.group.Options = append(b.group.Options, option)
	return b
}

// Required sets whether the user must select an option.
func (b *RadioGroupBuilder) Required(required bool) *RadioGroupBuilder {
	b.group.Required = &required
	return b
}

// Build returns the constructed modal component.
func (b *RadioGroupBuilder) Build() RadioGroup {
	group := b.group
	group.Options = slices.Clone(group.Options)
	return group
}

// CheckboxGroupBuilder constructs a modal checkbox group.
type CheckboxGroupBuilder struct {
	group CheckboxGroup
}

// NewCheckboxGroup starts a group with the given input ID.
func NewCheckboxGroup(customID string) *CheckboxGroupBuilder {
	return &CheckboxGroupBuilder{group: CheckboxGroup{CustomID: customID}}
}

// AddOption appends an available choice.
func (b *CheckboxGroupBuilder) AddOption(option ModalChoice) *CheckboxGroupBuilder {
	b.group.Options = append(b.group.Options, option)
	return b
}

// Required sets whether the user must make a selection.
func (b *CheckboxGroupBuilder) Required(required bool) *CheckboxGroupBuilder {
	b.group.Required = &required
	return b
}

// MinValues sets the minimum number of selected options.
func (b *CheckboxGroupBuilder) MinValues(min int) *CheckboxGroupBuilder {
	b.group.MinValues = &min
	return b
}

// MaxValues sets the maximum number of selected options.
func (b *CheckboxGroupBuilder) MaxValues(max int) *CheckboxGroupBuilder {
	b.group.MaxValues = max
	return b
}

// Build returns the constructed modal component.
func (b *CheckboxGroupBuilder) Build() CheckboxGroup {
	group := b.group
	group.Options = slices.Clone(group.Options)
	return group
}

// CheckboxBuilder constructs a modal checkbox.
type CheckboxBuilder struct {
	checkbox Checkbox
}

// NewCheckbox starts a checkbox with the given input ID.
func NewCheckbox(customID string) *CheckboxBuilder {
	return &CheckboxBuilder{checkbox: Checkbox{CustomID: customID}}
}

// Default sets whether the checkbox starts checked.
func (b *CheckboxBuilder) Default(checked bool) *CheckboxBuilder {
	b.checkbox.Default = checked
	return b
}

// Build returns the constructed modal component.
func (b *CheckboxBuilder) Build() Checkbox {
	return b.checkbox
}
