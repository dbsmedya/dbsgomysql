CREATE TABLE {{schema}}.cc_users (
  id INT PRIMARY KEY,
  email VARCHAR(50),
  UNIQUE KEY email (email));

CREATE TABLE {{schema}}.cc_contacts (
  id INT PRIMARY KEY,
  email VARCHAR(50),
  CONSTRAINT email CHECK (email <> ''));

CREATE TABLE {{schema}}.cc_parent (
  id INT PRIMARY KEY,
  a INT,
  b INT,
  UNIQUE KEY ab (a, b));

CREATE TABLE {{schema}}.cc_child (
  id INT PRIMARY KEY,
  pid INT,
  a INT,
  b INT,
  UNIQUE KEY k (pid),
  CONSTRAINT k FOREIGN KEY (pid) REFERENCES {{schema}}.cc_parent (id),
  CONSTRAINT k CHECK (pid >= 0),
  CONSTRAINT fk_ab FOREIGN KEY (a, b) REFERENCES {{schema}}.cc_parent (a, b));
