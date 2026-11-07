(function () {
  "use strict";

  var form = document.getElementById("compose-form");
  if (!form) return;

  var emoji = { urgent: "🚨", warning: "⚠️", info: "ℹ️", funny: "😂" };
  var label = { urgent: "Urgent", warning: "Warning", info: "Info", funny: "Funny" };

  var titleEl = document.getElementById("pv-title");
  var bodyEl = document.getElementById("pv-body");
  var sevEl = document.getElementById("pv-sev");
  var countEl = document.getElementById("byte-count");
  var confirmBox = document.getElementById("urgent-confirm");
  var confirmInput = confirmBox ? confirmBox.querySelector("input") : null;
  var submit = form.querySelector("button.primary");
  var imageInput = document.getElementById("image-input");
  var imagePreview = document.getElementById("pv-image");
  var imageURL = null;

  function value(name) {
    var el = form.elements[name];
    return el && typeof el.value === "string" ? el.value : "";
  }

  function selectedSeverity() {
    var checked = form.querySelector('input[name="severity"]:checked');
    return checked ? checked.value : "urgent";
  }

  function oneLine(text) {
    return String(text || "").trim().replace(/\s+/g, " ");
  }

  function update() {
    var sev = selectedSeverity();
    var headline = oneLine(value("headline"));
    var details = oneLine(value("details"));
    var link = String(value("link") || "").trim();

    var line = emoji[sev] + " " + (headline || "Headline") + " -- " + (details || "Details");
    if (link) line += " -- " + link;

    if (titleEl) titleEl.textContent = emoji[sev] + " " + (headline || "Headline");
    if (bodyEl) bodyEl.textContent = details || "Details";
    if (sevEl) sevEl.textContent = label[sev] || sev;
    if (countEl) countEl.textContent = String(new TextEncoder().encode(line).length);

    if (imagePreview) {
      if (imageURL) {
        URL.revokeObjectURL(imageURL);
        imageURL = null;
      }
      var file = imageInput && imageInput.files && imageInput.files[0];
      if (file) {
        imageURL = URL.createObjectURL(file);
        imagePreview.src = imageURL;
        imagePreview.hidden = false;
      } else if (sev === "funny") {
        imagePreview.src = "/static/bird.jpg";
        imagePreview.hidden = false;
      } else {
        imagePreview.removeAttribute("src");
        imagePreview.hidden = true;
      }
    }

    var urgent = sev === "urgent";
    if (confirmBox) confirmBox.classList.toggle("visible", urgent);
    if (confirmInput && !urgent) confirmInput.checked = false;
    if (submit) submit.disabled = urgent && confirmInput && !confirmInput.checked;
  }

  form.addEventListener("input", update);
  form.addEventListener("change", update);
  form.addEventListener("keydown", function (event) {
    if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
      event.preventDefault();
      if (!submit.disabled) form.requestSubmit();
    }
  });

  update();
})();
