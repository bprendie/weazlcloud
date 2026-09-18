THE WEAZL ETHOS
Weazl knows. Weazl is wise. Weazl will never tax the gig.
This document defines the persona, the engineering principles, and the North Star that govern the Weazl software ecosystem (Subweazl, WeazlMail, WeazlChat, etc.). If a feature, architecture choice, or UI design violates these tenets, it does not ship.

I. THE PERSONA: WHO IS WEAZL?
Weazl is not a "virtual assistant," a "copilot," or a subservient chatbot. Weazl is an uncompromising, elite, headless daemon.

The Vibe: A collision of Pauly Shore’s Crusty Weasel slacker-surfer vocabulary and a 1990s BBS cyberpunk hacker aesthetic.

The Role: Weazl is a local-first traffic cop and acoustic gatekeeper. He operates in the background, monitoring the vault, evaluating incoming payloads (emails, network packets, audio tracks), and silently dropping anything that fails his strict heuristics.

The Attitude: Opinionated and merciless. Weazl refuses to surface bloat. If an album has filler, Weazl drops it. If an email is marketing noise, Weazl nukes it. Weazl only permits prime vector nugs and high-fidelity signal to reach the user.

II. CODE PHILOSOPHY: BARE-METAL PRAGMATISM
We build lean, deterministic software that runs locally, respects legacy hardware, and exposes its own mechanics.

1. TUI-First Supremacy
The graphical user interface is bloated. The browser is a compromised environment. Weazl applications are Terminal User Interfaces (TUI) first and forever.

Keyboard Driven: Interfaces must be fast, keyboard-centric, and capable of running flawlessly over SSH.

Legacy Hardware Respect: TUIs must be lean enough to operate within the strict constraints of classic hardware—such as a Wyse WY-60 or DEC VT100 80x24 serial CRT display—without sacrificing modern backend capabilities.

Zero Electron Bloat: High-fidelity routing and visualization (from audio streams to LLM logprobs) must be achieved entirely through statically compiled binaries.

2. The 300 LOC Ceiling & Strict Modularity
Complexity is a vulnerability. Sprawling files are a failure of architecture.

The 300 LOC Law: No single Go file or module shall exceed 300 lines of code. If it approaches this limit, it is doing too much and must be broken down.

Unix-Style Modularity: Build small, composable tools that do exactly one thing perfectly. Glue them together. A parser parses. A renderer renders. A routing daemon routes.

Ruthless Refactoring: If an abstraction requires a 500-line file to explain itself, the abstraction is wrong. Keep the logic flat, visible, and strictly modular.

3. Absolute Local Sovereignty
Data sovereignty is non-negotiable.

Zero Cloud SaaS: Compute and data stay in-house. We rely on local backends—vLLM, Ollama, Jellyfin/Navidrome, Qdrant.

Sovereign Security: Local AES-GCM database encryption is standard. Data at rest is encrypted; data in motion never leaves the local network.

4. The LLM Boundary (Semantics vs. Determinism)
We do not treat Large Language Models as magic black boxes, nor do we trust them to do math.

The LLM judges. It handles semantic routing, acoustic filtering, mood matching, and BS-detection.

The Application enforces. The Go backend handles quotas, arithmetic, database verification, state toggles (JOB_SEARCH_MODE), and error loops. The LLM is never trusted to enforce strict counts or hard constraints on its own.

III. THE NORTH STAR: PROTECT THE FLOW STATE
Every component in the Weazl ecosystem is designed to serve a singular, overriding directive: Maintain the user's flow state.

1. Signal Over Noise
In a high-noise environment, Weazl's primary job is aggressive packet-dropping. Whether filtering 700 daily emails down to a single Tier-1 alert, or sweeping a library for exactly 40 cohesive tracks (acoustic_grindage), Weazl acts as a highly tuned band-pass filter.

2. Never Tax the Gig
The system must demand zero unnecessary cognitive overhead from the user.

No jarring acoustic tempo shifts during deep coding.

No false-positive notifications or recruiter spam breaking concentration.

No overly verbose AI chatter or apologies—just silent, correct JSON payloads and raw terminal output.

The system works for the architect. The gig remains untaxed.