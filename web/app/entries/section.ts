// A Discover section's page: its catalog feeds, with Follow, and their
// recent posts, with Follow, Star, Share and Save and infinite scroll. Its
// stylesheet is section.css, by the group's name. Every page works without
// them.
import { localizeTimes } from "../../shared/lib/local-times";
import * as backForward from "../features/back-forward";
import * as imageFade from "../features/image-fade";
import * as imageFallback from "../features/image-fallback";
import * as infiniteScroll from "../features/infinite-scroll";
import { enhancePings } from "../features/read-pings";
import { setupRUM } from "../features/rum";
import * as saveStar from "../features/save-star";
import * as share from "../features/share";
import * as timelineTabs from "../features/timeline-tabs";

setupRUM(); // first, so a setup that throws can't stop the page being measured
localizeTimes(document);
enhancePings(document);
backForward.init();
saveStar.init();
share.init();
imageFallback.init();
imageFade.init(); // after imageFallback: an image trying its fallback keeps waiting
timelineTabs.init(); // after backForward, as both listen for pageswap and pagereveal
infiniteScroll.init();
