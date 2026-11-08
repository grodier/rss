// The timeline form (form[data-timeline-form]): rebuild the summary above
// it ([data-summary-sentence], [data-summary-count], [data-summary-feeds])
// and the counts in it as fields change, with the same rules and words as
// timelineSentence, timelineFeedCount and timelineFormSummary in
// timelines.go (change them together). Feeds carry their name, tag IDs and
// posting rate in data-*; tags their name. Also add a filter box above
// each feed list ([data-feed-list]).

// A feed the form lists: its ID, name, tag IDs and posts a week.
export interface Feed {
  id: string;
  name: string;
  tags: string[];
  rate: number;
}

// A tag the form lists.
export interface Tag {
  id: string;
  name: string;
}

// The form's fields, as summary reads them (timelineForm's in timelines.go).
export interface Fields {
  show: string; // "all" or "some"
  includeTags: Tag[]; // the ticked tags, in the form's order
  excludeTags: Tag[];
  includeFeeds: string[]; // the ticked feeds' IDs
  excludeFeeds: string[];
  starredOnly: boolean;
  mediaOnly: boolean;
  mentions: boolean;
  includeWords: string;
  notMentions: boolean;
  excludeWords: string;
  quiet: boolean;
  maxPostsPerWeek: string;
}

// The summary box (timelineFormSummary in timelines.go).
export interface Summary {
  sentence: string;
  count: string; // "From 4 of your 14 feeds"
  feeds: string[]; // the matching feeds' names
  rateHint: string; // under "At most N posts a week"; "" when N isn't valid
}

export const joinWith =
  (word: string) =>
  (items: string[]): string =>
    items.length < 2 ? items.join("") : `${items.slice(0, -1).join(", ")} ${word} ${items[items.length - 1]}`;
export const joinOr = joinWith("or");
export const joinAnd = joinWith("and");

// splitTerms: trimmed, inner spaces collapsed, no empties or repeats.
export function terms(s: string): string[] {
  const seen = new Set<string>();
  return s
    .split(",")
    .map((t) => t.trim().split(/\s+/).join(" "))
    .filter((t) => {
      const k = t.toLowerCase();
      if (!t || seen.has(k)) return false;
      seen.add(k);
      return true;
    });
}

export const quoted = (ws: string[]): string[] => ws.map((w) => `“${w}”`);

// maxPostsPerWeek: a whole number from 1 to 100, else 0.
export function rate(value: string): number {
  const v = value.trim();
  const n = Number(v);
  return /^\d+$/.test(v) && n >= 1 && n <= 100 ? n : 0;
}

// summary is the box above the form for fields, given every feed in the
// user's order.
export function summary(feeds: Feed[], fields: Fields): Summary {
  const some = fields.show === "some";
  const inTags = fields.includeTags,
    exTags = fields.excludeTags;
  const inFeedIDs = fields.includeFeeds,
    exFeedIDs = fields.excludeFeeds;
  const inTagIDs = inTags.map((t) => t.id),
    exTagIDs = exTags.map((t) => t.id);
  const feedNames = (ids: string[]) => feeds.filter((f) => ids.includes(f.id)).map((f) => f.name);
  const n = rate(fields.maxPostsPerWeek);
  const quiet = fields.quiet ? n : 0;

  let sentence: string,
    count: string,
    matched: Feed[] = [];
  if (some && !inTags.length && !inFeedIDs.length) {
    sentence = "Choose at least one tag or feed, or show all your feeds.";
    count = "No feeds yet";
  } else {
    let s = "Articles";
    if (fields.starredOnly) s += " you’ve starred";
    if (fields.mediaOnly) s += " with audio or video";
    const words = fields.mentions ? quoted(terms(fields.includeWords)) : [];
    const notWords = fields.notMentions ? quoted(terms(fields.excludeWords)) : [];
    if (words.length) s += ` mentioning ${joinOr(words)}`;
    if (notWords.length) s += `${words.length ? " but not " : " not mentioning "}${joinOr(notWords)}`;
    const rateText = quiet ? `post at most ${quiet === 1 ? "once a week" : `${quiet} times a week`}` : "";
    if (!some) {
      s += rateText ? ` from feeds that ${rateText}` : " from all your feeds";
    } else {
      const src = [];
      if (inTags.length) src.push(`feeds tagged ${joinOr(inTags.map((t) => t.name))}`);
      src.push(...feedNames(inFeedIDs));
      s += ` from ${joinAnd(src)}`;
      if (rateText) s += inFeedIDs.length ? `, if they ${rateText}` : ` that ${rateText}`;
    }
    const except = [];
    if (exTags.length) except.push(`feeds tagged ${joinOr(exTags.map((t) => t.name))}`);
    except.push(...feedNames(exFeedIDs));
    if (except.length) s += `, except ${joinAnd(except)}`;
    s += ".";
    if (fields.starredOnly) s += " Newest star first.";
    sentence = s;

    matched = feeds.filter((f) => {
      const shown = !some || f.tags.some((t) => inTagIDs.includes(t)) || inFeedIDs.includes(f.id);
      const out = f.tags.some((t) => exTagIDs.includes(t)) || exFeedIDs.includes(f.id);
      return shown && !out && (!quiet || f.rate <= quiet);
    });
    if (!matched.length) count = "None of your feeds match yet";
    else if (feeds.length === 1) count = "From your only feed";
    else count = `From ${matched.length} of your ${feeds.length} feeds`;
  }

  const k = feeds.filter((f) => n && f.rate <= n).length;
  const rateHint = !n
    ? ""
    : k === 1
      ? "1 of your feeds posts this little right now."
      : `${k} of your feeds post this little right now.`;
  return { sentence, count, feeds: matched.map((f) => f.name), rateHint };
}

export function init(): void {
  const form = document.querySelector<HTMLFormElement>("form[data-timeline-form]");
  if (!form) return;
  const checked = (name: string) => [...form.querySelectorAll<HTMLInputElement>(`input[name="${name}"]:checked`)];
  const box = (name: string) => form.querySelector<HTMLInputElement>(`input[name="${name}"]`)?.checked ?? false;
  const value = (name: string) => (form.elements.namedItem(name) as RadioNodeList | HTMLInputElement).value;
  const tag = (el: HTMLInputElement): Tag => ({ id: el.value, name: el.dataset.name ?? "" });
  // Every feed, in the user's order, from the Show list (or Leave out's).
  const feeds = [...form.querySelectorAll<HTMLInputElement>('input[name="include_feeds"], input[name="exclude_feeds"]')]
    .filter((el, i, all) => all.findIndex((o) => o.value === el.value) === i)
    .map((el) => ({
      id: el.value,
      name: el.dataset.name ?? "",
      tags: (el.dataset.tags || "").split(" ").filter(Boolean),
      rate: Number(el.dataset.rate),
    }));

  const update = () => {
    const inFeedIDs = checked("include_feeds").map((el) => el.value),
      exFeedIDs = checked("exclude_feeds").map((el) => el.value);
    const exTags = checked("exclude_tags");
    const sum = summary(feeds, {
      show: value("show"),
      includeTags: checked("include_tags").map(tag),
      excludeTags: exTags.map(tag),
      includeFeeds: inFeedIDs,
      excludeFeeds: exFeedIDs,
      starredOnly: box("starred_only"),
      mediaOnly: box("media_only"),
      mentions: box("mentions"),
      includeWords: value("include_words"),
      notMentions: box("not_mentions"),
      excludeWords: value("exclude_words"),
      quiet: box("quiet"),
      maxPostsPerWeek: value("max_posts_per_week"),
    });

    const parent = form.parentElement!;
    parent.querySelector("[data-summary-sentence]")!.textContent = sum.sentence;
    parent.querySelector("[data-summary-count]")!.textContent = sum.count;
    const list = parent.querySelector("[data-summary-feeds]")!;
    list.replaceChildren(
      ...sum.feeds.map((name) => Object.assign(document.createElement("li"), { textContent: name })),
    );

    const hint = form.querySelector("[data-rate-hint]");
    if (hint) hint.textContent = sum.rateHint;
    const label = (sel: string, k: number) => {
      const el = form.querySelector(sel);
      if (el) el.textContent = k ? `(${k})` : "";
    };
    label("[data-feeds-count]", inFeedIDs.length);
    label("[data-leave-out-count]", exTags.length + exFeedIDs.length);
  };
  form.addEventListener("input", update);
  form.addEventListener("change", update);

  for (const list of form.querySelectorAll<HTMLElement>("ul[data-feed-list]")) {
    const filter = Object.assign(document.createElement("input"), {
      type: "search",
      className: "feed-filter",
      placeholder: "Filter feeds",
    });
    filter.setAttribute("aria-label", "Filter feeds");
    filter.addEventListener("input", () => {
      const q = filter.value.trim().toLowerCase();
      for (const li of list.children as HTMLCollectionOf<HTMLElement>)
        li.hidden = q !== "" && !li.dataset.name!.toLowerCase().includes(q);
    });
    // The filter isn't a field: it doesn't rebuild the summary, and
    // Enter in it doesn't submit the form.
    filter.addEventListener("input", (e) => e.stopPropagation());
    filter.addEventListener("keydown", (e) => {
      if (e.key === "Enter") e.preventDefault();
    });
    list.before(filter);
  }
}
