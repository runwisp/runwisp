// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export const RADIO_GROUP_KEY = Symbol("radio-group");

// What a RadioGroup hands its Radio children: the shared input name and the
// selected value, read and written through the group's bindable `value`.
export interface RadioGroupContext {
    readonly name: string;
    value: string;
}
