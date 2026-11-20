// Each panel module is imported here for its definePanel registration: a panel
// registers when its module is imported, so a part that adds a panel adds a line.
// A route with no panel registered draws the placeholder.
import "./fleet";
import "./turns";
import "./info";
import "./changes";
import "./agent";
