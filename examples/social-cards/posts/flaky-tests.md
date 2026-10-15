---
title: A Field Guide to Flaky Tests
description: Five patterns that make a test pass on Monday and fail on Friday, and how to pin each one down.
tags: [testing, ci]
art: a glowing coin spinning in mid air, one face teal and the other amber, with soft motion trails and drifting paper confetti
---

A flaky test is a coin toss in your build. This guide names five patterns: shared state, clocks, ordering, timeouts and the network.

For each pattern you get one way to spot it and one way to fix it.
