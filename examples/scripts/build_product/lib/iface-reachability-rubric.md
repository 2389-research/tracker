# Interface Reachability Rubric (issue #233 Gap 7)

## When this rubric applies (language detection)

Survey languages in this project:
  git ls-files | awk -F. '{print $NF}' | sort -u

If the project contains files in a static-interface language —
Go (.go), Rust (.rs), Java (.java), Kotlin (.kt), Swift (.swift),
TypeScript (.ts/.tsx), C++ (.cc/.cpp/.cxx/.hh/.hpp/.hxx),
C# (.cs), PHP (.php), Python with `ABC` or `Protocol` (.py) —
proceed. Note: `.h` alone is NOT a C++ trigger because the
extension is ambiguous between C (no static interfaces) and
C++ (has them). Header-only C++ projects using only `.h` are
a known blind spot — operators can declare via `.ai/decisions/`
or rename to `.hpp`/`.hxx`. The enumeration grep `--include`
list does include `.h` so that mixed-source C++ projects with
`.cpp` + `.h` still get their headers scanned.

If the project is exclusively in languages WITHOUT a static
interface system (Ruby, plain JS, Elixir, Zig, C without
function-pointer-table conventions, bash, shell, plain Markdown,
DSL files), skip this check with a one-line note: "no
static-interface languages detected; reachability check skipped."

## Enumeration

For each detected language, enumerate interfaces / protocols /
traits / typeclasses / abstract classes:
  Go:      grep -rnE 'type [[:alnum:]_]+ +interface[[:space:]{]' \
             --include='*.go' .
           Catches exported and unexported names; allows brace
           immediately after `interface` (no space). Generic
           interfaces (`type Foo[T any] interface`) are rarer
           and not enumerated by this pattern — if the project
           uses them, run a follow-up grep with the bracket
           syntax.
  Rust:    grep -rnE '(^|[^[:alnum:]_])trait +[[:alnum:]_]+' \
             --include='*.rs' .
           Catches `trait`, `pub trait`, `pub(crate) trait`,
           and unexported traits — all can carry unwired
           methods. The `(^|[^[:alnum:]_])` prefix is a
           portable word-boundary (POSIX ERE doesn't define
           `\b`; GNU grep supports it but BSD/macOS does not).
  Java:    grep -rnE '^(public |abstract |sealed )*(interface |abstract class )' \
             --include='*.java' .
  Kotlin:  grep -rnE '(interface |abstract class |fun interface )' \
             --include='*.kt' .
  Swift:   grep -rnE 'protocol [A-Z][A-Za-z0-9_]* *(:|\{)' \
             --include='*.swift' .
  TS:      grep -rnE '(interface |abstract class )' \
             --include='*.ts' --include='*.tsx' .
  C++:     best-effort — look for pure-virtual classes:
             grep -rnE 'virtual [^;]+= *0 *;' \
             --include='*.cc' --include='*.cpp' --include='*.cxx' \
             --include='*.h' --include='*.hh' --include='*.hpp' \
             --include='*.hxx' .
           The containing class is an abstract interface. CRTP
           and templates are not enumerable via grep; skip those
           with a one-line note.
  C#:      grep -rnE '(interface |abstract class )' \
             --include='*.cs' .
  PHP:     grep -rnE '(interface |abstract function )' \
             --include='*.php' .
  Python:  grep -rnE 'class [A-Z][A-Za-z0-9_]*\(.*(ABC|Protocol).*\)' \
             --include='*.py' .

Skip constraint-only Go interfaces (`interface { ~int | ~string }`).
Skip embedded interface methods (those inherited from another
interface declared elsewhere are out of scope; they're checked
where they're declared).

## Caller discipline

For each declared method M, find a non-test production caller.

GREP CALL SYNTAX, NOT METHOD NAME. The grep must target an
invocation: `\.M(` (Go/Java/TS/Swift/Kotlin/C#/PHP),
`\.method_name(` (Python/Ruby), `->M(` (C++ via pointer),
`::M(` (Rust via type), etc. The cited file:line MUST be a
call expression, not a declaration line and not a receiver
definition line.

When M's name is common (Close, Read, String, Error, Run, Send,
Get, Set, Init, New, ToString), the grep MUST include receiver
context — e.g.
  grep -B1 -A0 '\.M(' --include='*.go' . | grep -v _test.go
and the cited line must show the receiver type alongside the
call. Same-name collisions are the dominant false-positive shape.

EXCLUSIONS — these do NOT count as production callers:
  *_test.go, **/testutil/**, **/testing/**, **/mocks/**,
  **/fakes/**, **/fixtures/**, **/testdata/**, *_mock.go,
  **/__tests__/**, *.test.*, *.spec.*, conftest.py,
  src/test/java/**, test/*_test.exs, spec/**, Tests/, tests/,
  .gocache/**.
Note: build-output trees (`target/**`, `build/**`) are NOT
blanket-excluded — they often contain generated-source callers
(see next paragraph) that ARE production. The test-specific
globs above (`src/test/java/**`, etc.) handle hand-written test
trees regardless of where they live.

Generated code (*.pb.go, *_gen.go, zz_generated_*.go,
target/generated-sources/**, __generated__/**, *.pb.cc,
moc_*.cpp) DOES count as a production caller — note it
explicitly when relevant.

## Stdlib / framework satisfaction (principle)

If the implementing type is passed to a stdlib or framework
function that accepts an interface argument, cite that passing
site as the production caller. Examples:
  Go:     http.Handle(p, h), bufio.NewReader(r),
          sql.Register(n, d), json.Marshal(v), sort.Sort(s),
          slog.New(h), flag.Var(v, n, u)
  Python: passing to iter(), next(), with, framework
          decorators (@app.route, @app.task)
  TS:     passing to APIs typed with Iterable<T>, PromiseLike<T>
  Java:   Spring @Component / @Autowired / @RestController,
          JAX-RS @Path, ServiceLoader via META-INF/services
  Swift:  SwiftUI body protocol (framework calls it),
          Combine subscribers
This is non-exhaustive; the principle is "framework or stdlib
consumes the interface" → cite the wiring site, not a direct
method call.

## Waivers

A missing production caller is FAIL unless `.ai/decisions/*.md`
documents this specific method by name with a non-blanket
rationale. Before declaring FAIL run:
  grep -rn '<MethodName>' .ai/decisions/
A hit naming the method with a rationale that wouldn't apply
equally to every method (so not just "API surface" / "future
use" / "by design") → PASS, cite the decision-log path. Blanket
waivers → still FAIL.

## Library-API carve-out

If `.ai/decisions/library_api.md` exists and lists this
method's interface OR receiver type, treat as PASS and cite
the declaration line. This is for public library symbols whose
production callers are external (downstream) consumers.

## Known limitations — skip with one-line note, not FAIL

Some dispatch mechanisms are fundamentally outside grep's reach.
Skip these with a one-line note rather than flagging:
  Rust `dyn Trait` / `Box<dyn T>` collections (cite the trait-
    object site as the linkage if visible)
  Haskell typeclass dispatch (cite a constrained function)
  TypeScript bracket-notation dispatch: obj['method']()
  Java sealed interfaces with exhaustive switch dispatch
  Swift `extension Type: Protocol` conformances added away
    from the type's declaration site
  Ruby module mixins (not a static interface system)
  Elixir / Erlang @behaviour callbacks (called by OTP)
  Go interface-typed parameter dispatch where the static type
    can't be proven — show the grep you ran and cite the
    parameter site; do NOT use this as a default carve-out.

When you skip, name the specific reason. Don't blanket-skip an
entire interface as "dynamic dispatch."
