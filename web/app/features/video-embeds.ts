// Video embeds: figure.embed[data-embed] (sanitize's videoEmbed) holds a
// link to the video. On a plain click, swap the link for the player, built
// from the validated data-embed-id, never from a URL in the feed.
// Delegated, so it covers any article on the page.
interface Player {
  title: string;
  src: (id: string) => string;
}

const players: Record<string, Player | undefined> = {
  youtube: { title: "YouTube video", src: (id) => `https://www.youtube-nocookie.com/embed/${id}?autoplay=1` },
  vimeo: { title: "Vimeo video", src: (id) => `https://player.vimeo.com/video/${id}?autoplay=1&dnt=1` },
};

export function init(): void {
  document.addEventListener("click", (e) => {
    if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
    const a = e.target instanceof Element && e.target.closest("figure.embed[data-embed] a");
    if (!a) return;
    const figure = a.closest("figure")!;
    const player = players[String(figure.dataset.embed)];
    const id = figure.dataset.embedId;
    if (!player || !id || !/^[A-Za-z0-9_-]{1,20}$/.test(id)) return;
    e.preventDefault();
    const iframe = document.createElement("iframe");
    iframe.src = player.src(encodeURIComponent(id));
    iframe.title = player.title;
    iframe.allow = "autoplay; encrypted-media; fullscreen; picture-in-picture";
    iframe.allowFullscreen = true;
    iframe.setAttribute("sandbox", "allow-scripts allow-same-origin allow-presentation allow-popups");
    iframe.referrerPolicy = "strict-origin-when-cross-origin";
    a.replaceWith(iframe);
    iframe.focus();
  });
}
