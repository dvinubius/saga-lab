const localTime = (datetime) =>
  new Date(datetime).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", fractionalSecondDigits: 3 });

const localTimes = (root) => root.querySelectorAll("time[datetime]").forEach((time) => (time.textContent = localTime(time.dateTime)));

addEventListener("DOMContentLoaded", () => localTimes(document));
