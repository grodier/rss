// The admin pages' enhancements. Their stylesheet is styles/base.css, which
// every admin page links. Every page works without them.
import * as busyForms from "../../shared/lib/busy-forms";
import * as select from "../features/select";

busyForms.init();
select.init();
