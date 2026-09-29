// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux && !darwin

package bootid

// Current returns "": this platform exposes no boot identity.
func Current() string { return "" }
