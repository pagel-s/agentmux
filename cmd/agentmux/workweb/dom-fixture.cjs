"use strict";

// The small DOM contract used by polling/focus regressions. Removing a focused
// descendant returns focus to body, as in a browser; production renderers and
// their focus/disclosure restoration run unchanged. No application logic lives
// in this fixture and no browser, model or provider is launched.
function domFixture() {
  const document = { activeElement: null };
  const camel = (name) => name.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
  class Node {
    constructor(tag, className = "", text = "") {
      this.tagName = tag;
      this.className = className;
      this.textContent = text;
      this.children = [];
      this.parentNode = null;
      this.dataset = {};
      this.style = {};
      this.attributes = {};
      this.value = "";
      this.disabled = false;
      this.open = false;
      this.scrollTop = this.scrollLeft = 0;
      this.listeners = {};
      this.classList = {
        toggle: (name, force) => {
          const names = new Set(this.className.split(/\s+/).filter(Boolean));
          const enabled = force === undefined ? !names.has(name) : force;
          if (enabled) names.add(name);
          else names.delete(name);
          this.className = [...names].join(" ");
          return enabled;
        },
        add: (name) => this.classList.toggle(name, true),
        remove: (name) => this.classList.toggle(name, false),
      };
    }
    get childElementCount() {
      return this.children.length;
    }
    get options() {
      return this.children.filter((node) => node.tagName === "option");
    }
    append(...nodes) {
      for (const node of nodes) {
        if (node.parentNode)
          node.parentNode.children = node.parentNode.children.filter(
            (child) => child !== node,
          );
        node.parentNode = this;
        this.children.push(node);
      }
    }
    replaceChildren(...nodes) {
      if (this.children.some((node) => node.contains(document.activeElement)))
        document.activeElement = document.body;
      for (const node of this.children) node.parentNode = null;
      this.children = [];
      this.append(...nodes);
    }
    contains(node) {
      for (let current = node; current; current = current.parentNode)
        if (current === this) return true;
      return false;
    }
    matches(selector) {
      return selector.split(",").some((part) => {
        const value = part.trim();
        if (value.startsWith("."))
          return this.className.split(/\s+/).includes(value.slice(1));
        if (value.startsWith("[data-") && value.endsWith("]"))
          return this.dataset[camel(value.slice(6, -1))] !== undefined;
        return value === this.tagName;
      });
    }
    closest(selector) {
      for (let node = this; node; node = node.parentNode)
        if (node.matches(selector)) return node;
      return null;
    }
    querySelectorAll(selector) {
      const results = [];
      for (const child of this.children) {
        if (child.matches(selector)) results.push(child);
        results.push(...child.querySelectorAll(selector));
      }
      return results;
    }
    setAttribute(name, value) {
      this.attributes[name] = String(value);
      if (name.startsWith("data-"))
        this.dataset[camel(name.slice(5))] = String(value);
      if (name === "class") this.className = String(value);
    }
    getAttribute(name) {
      return this.attributes[name];
    }
    focus(options = {}) {
      if (this.disabled) return;
      document.activeElement = this;
      if (!options.preventScroll) {
        const scroller = this.closest(".graph-canvas,.inspector,.view");
        if (scroller) scroller.scrollTop = 0;
      }
    }
    click() {
      if (!this.disabled) this.onclick?.({ preventDefault() {} });
    }
    addEventListener(name, handler) {
      this.listeners[name] = handler;
    }
  }
  document.body = new Node("body");
  document.activeElement = document.body;
  document.createElement = (tag) => new Node(tag);
  document.createElementNS = (_ns, tag) => new Node(tag);
  document.createTextNode = (text) => new Node("#text", "", text);
  document.querySelectorAll = (selector) =>
    document.body.querySelectorAll(selector);
  const nodes = new Map();
  const node = (id, className = "") => {
    const live = document.body
      .querySelectorAll("[data-fixture-id]")
      .find((item) => item.dataset.fixtureId === id);
    if (live) return live;
    if (!nodes.has(id)) {
      const item = new Node("div", className);
      item.id = id;
      item.dataset.fixtureId = id;
      document.body.append(item);
      nodes.set(id, item);
    }
    return nodes.get(id);
  };
  return {
    document,
    node,
    el: (tag, cls, text) => new Node(tag, cls, text),
    Node,
  };
}
module.exports = { domFixture };
