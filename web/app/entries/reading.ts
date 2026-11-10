// The reading pages' enhancements (timelines, Saved, feeds, sites,
// articles and the debug inspectors), set up in this order: it matters
// where two listen for the same event. Their stylesheet is reading.css, by
// the group's name. Every page works without them.
import * as busyForms from "../../shared/lib/busy-forms";
import { localizeTimes } from "../../shared/lib/local-times";
import * as backForward from "../features/back-forward";
import * as dividerDraw from "../features/divider-draw";
import * as infiniteScroll from "../features/infinite-scroll";
import { enhancePings } from "../features/read-pings";
import { setupRUM } from "../features/rum";
import * as saveStar from "../features/save-star";
import * as seenBeacon from "../features/seen-beacon";
import * as share from "../features/share";
import * as timelineTabs from "../features/timeline-tabs";
import * as videoEmbeds from "../features/video-embeds";

setupRUM(); // first, so a setup that throws can't stop the page being measured
localizeTimes(document);
enhancePings(document);
backForward.init();
saveStar.init();
share.init();
busyForms.init(); // after saveStar: it skips the submits saveStar takes over
videoEmbeds.init();
timelineTabs.init(); // after backForward, as both listen for pageswap and pagereveal
seenBeacon.init();
dividerDraw.init(); // before infiniteScroll, which draws the dividers it appends
infiniteScroll.init();
