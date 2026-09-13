// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Gaurav Mishra

// The public schema is shared with collectors and senders.
package platform

import schema "rubberai/internal/event"

type Event = schema.Event
type Named = schema.Named
type Model = schema.Model
type Usage = schema.Usage
type Cost = schema.Cost
type Rates = schema.Rates
type File = schema.File

var Estimate = schema.Estimate
