import { module, test } from 'qunit';
import { setupTest } from 'ember-qunit';
import ApplicationSerializer from 'nomad-ui/serializers/application';

import Model from 'ember-data/model';
import attr from 'ember-data/attr';

class TestSerializer extends ApplicationSerializer {
  arrayNullOverrides = ['Things'];

  mapToArray = [
    'ArrayableMap',
    { APIName: 'APINameArrayableMap', UIName: 'RenamedArrayableMap' },
    {
      name: 'ConvertedArrayableMap',
      convertor: (apiHashMember, uiHashMember, mapKey, apiHash) => {
        Object.keys(apiHashMember).forEach(key => {
          uiHashMember.SomethingExtra = apiHash.SomethingExtra;
          uiHashMember.AlsoName = mapKey;
          uiHashMember[`${key}${key}`] = apiHashMember[key];
        });
      },
    },
  ];

  separateNanos = ['Time'];
}

class TestModel extends Model {
  @attr() things;

  @attr() arrayableMap;
  @attr() renamedArrayableMap;
  @attr() convertedArrayableMap;

  @attr() time;
  @attr() timeNanos;
}

module('Unit | Serializer | Application', function(hooks) {
  setupTest(hooks);

  hooks.beforeEach(function() {
    this.store = this.owner.lookup('service:store');
    this.owner.register('model:test', TestModel);
    this.owner.register('serializer:test', TestSerializer);

    this.subject = () => this.store.serializerFor('test');
  });

  const normalizationTestCases = [
    {
      name: 'Null array and maps',
      in: {
        ID: 'test-test',
        Things: null,
        ArrayableMap: null,
        APINameArrayableMap: null,
        ConvertedArrayableMap: null,
        Time: 1607839992000100000,
      },
      out: {
        data: {
          id: 'test-test',
          attributes: {
            things: [],
            arrayableMap: [],
            renamedArrayableMap: [],
            convertedArrayableMap: [],
            time: 1607839992000,
            timeNanos: 100096,
          },
          relationships: {},
          type: 'test',
        },
      },
    },
    {
      name: 'Non-null array and maps',
      in: {
        ID: 'test-test',
        Things: [1, 2, 3],
        ArrayableMap: {
          a: { Order: 1 },
          b: { Order: 2 },
          'c.d': { Order: 3 },
        },
        APINameArrayableMap: {
          a: { X: 1 },
        },
        ConvertedArrayableMap: {
          a: { X: 1, Y: 2 },
        },
        Time: 1607839992000100000,
        SomethingExtra: 'xyz',
      },
      out: {
        data: {
          id: 'test-test',
          attributes: {
            things: [1, 2, 3],
            arrayableMap: [
              { Name: 'a', Order: 1 },
              { Name: 'b', Order: 2 },
              { Name: 'c.d', Order: 3 },
            ],
            renamedArrayableMap: [{ Name: 'a', X: 1 }],
            convertedArrayableMap: [
              {
                Name: 'a',
                AlsoName: 'a',
                XX: 1,
                YY: 2,
                SomethingExtra: 'xyz',
              },
            ],
            time: 1607839992000,
            timeNanos: 100096,
          },
          relationships: {},
          type: 'test',
        },
      },
    },
  ];

  normalizationTestCases.forEach(testCase => {
    test(`normalization: ${testCase.name}`, async function(assert) {
      assert.deepEqual(this.subject().normalize(TestModel, testCase.in), testCase.out);
    });
  });
});
