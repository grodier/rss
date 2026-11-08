// The reader's progressive enhancements, set up on every page in this
// order (it matters where two listen for the same event). Every page works
// without them.
import * as busyForms from "../../shared/lib/busy-forms";
import { localizeTimes } from "../../shared/lib/local-times";
import * as passwordToggle from "../../shared/lib/password-toggle";
import * as backForward from "../features/back-forward";
import * as importStatus from "../features/import-status";
import * as infiniteScroll from "../features/infinite-scroll";
import * as liveSearch from "../features/live-search";
import * as lookupStatus from "../features/lookup-status";
import { enhancePings } from "../features/read-pings";
import * as reorder from "../features/reorder";
import * as saveStar from "../features/save-star";
import * as seenBeacon from "../features/seen-beacon";
import * as share from "../features/share";
import * as timelineForm from "../features/timeline-form";
import * as timelineTabs from "../features/timeline-tabs";
import * as videoEmbeds from "../features/video-embeds";

localizeTimes(document);
enhancePings(document);
backForward.init();
saveStar.init();
share.init();
busyForms.init(); // after saveStar: it skips the submits saveStar takes over
liveSearch.init();
lookupStatus.init();
importStatus.init();
videoEmbeds.init();
passwordToggle.init();
timelineTabs.init(); // after backForward, as both listen for pageswap and pagereveal
timelineForm.init();
reorder.init();
seenBeacon.init();
infiniteScroll.init();
